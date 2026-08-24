package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const maxCompletionCandidates = 200
const maxBashRCSize = 1 << 20

var completionRequestTimeout = 2 * time.Second

const bashCompletionSource = "source <(bashido completion bash)"

const bashCompletion = `_bashido() {
  local cur prev top sub candidate state word option value
  local i positional=0 skip=0 start=1
  COMPREPLY=()
  cur=${COMP_WORDS[COMP_CWORD]}
  prev=${COMP_WORDS[COMP_CWORD-1]-}
  if [[ ${COMP_WORDS[1]} == --profile ]]; then
    start=3
  elif [[ ${COMP_WORDS[1]} == --profile=* ]]; then
    start=2
  fi
  top=${COMP_WORDS[start]-}
  sub=${COMP_WORDS[start+1]-}

  if (( COMP_CWORD == start )); then
    COMPREPLY=( $(compgen -W 'auth profile script note completion uninstall upgrade version help --help -h --profile' -- "$cur") )
    return
  fi

  case "$prev" in
    --ca-file|--notes-file)
      compopt -o filenames
      while IFS= read -r candidate; do COMPREPLY+=("$candidate"); done < <(compgen -f -- "$cur")
      return
      ;;
    --title)
      return
      ;;
    --profile)
      while IFS= read -r candidate; do
        [[ $candidate == "$cur"* ]] && COMPREPLY+=("$candidate")
      done < <("${COMP_WORDS[0]}" __complete profiles 2>/dev/null)
      return
      ;;
  esac

  if (( COMP_CWORD == start+1 )); then
    case "$top" in
      auth) COMPREPLY=( $(compgen -W 'login status logout' -- "$cur") ) ;;
      profile) COMPREPLY=( $(compgen -W 'list add use remove' -- "$cur") ) ;;
      script) COMPREPLY=( $(compgen -W 'list search show create update edit delete restore purge' -- "$cur") ) ;;
      note) COMPREPLY=( $(compgen -W 'show set edit clear' -- "$cur") ) ;;
      completion) COMPREPLY=( $(compgen -W 'bash install' -- "$cur") ) ;;
      uninstall) COMPREPLY=( $(compgen -W '--local-only --yes --help -h --profile' -- "$cur") ) ;;
    esac
    return
  fi

  case "$cur" in
    --ca-file=*|--notes-file=*)
      option=${cur%%=*}
      value=${cur#*=}
      compopt -o filenames
      while IFS= read -r candidate; do COMPREPLY+=("$option=$candidate"); done < <(compgen -f -- "$value")
      return
      ;;
    --profile=*)
      option=${cur%%=*}
      value=${cur#*=}
      while IFS= read -r candidate; do
        [[ $candidate == "$value"* ]] && COMPREPLY+=("$option=$candidate")
      done < <("${COMP_WORDS[0]}" __complete profiles 2>/dev/null)
      return
      ;;
    --title=*)
      return
      ;;
  esac

  if [[ $cur == -* ]]; then
    if [[ $top == uninstall ]]; then
      COMPREPLY=( $(compgen -W '--local-only --yes --help -h --profile' -- "$cur") )
      return
    fi
    case "$top:$sub" in
      auth:login) COMPREPLY=( $(compgen -W '--no-browser --replace --help -h --profile' -- "$cur") ) ;;
      auth:logout) COMPREPLY=( $(compgen -W '--local-only --help -h --profile' -- "$cur") ) ;;
      profile:add) COMPREPLY=( $(compgen -W '--ca-file --use --help -h --profile' -- "$cur") ) ;;
      profile:remove) COMPREPLY=( $(compgen -W '--local-only --yes --help -h --profile' -- "$cur") ) ;;
      script:list|script:search) COMPREPLY=( $(compgen -W '--trash --all --json --help -h --profile' -- "$cur") ) ;;
      script:show|note:show) COMPREPLY=( $(compgen -W '--json --help -h --profile' -- "$cur") ) ;;
      script:create) COMPREPLY=( $(compgen -W '--title --notes-file --help -h --profile' -- "$cur") ) ;;
      script:update) COMPREPLY=( $(compgen -W '--title --force --help -h --profile' -- "$cur") ) ;;
      script:edit|note:edit) COMPREPLY=( $(compgen -W '--force --help -h --profile' -- "$cur") ) ;;
      script:delete|script:restore) COMPREPLY=( $(compgen -W '--help -h --profile' -- "$cur") ) ;;
      script:purge|note:clear) COMPREPLY=( $(compgen -W '--yes --help -h --profile' -- "$cur") ) ;;
    esac
    return
  fi

  for ((i = start+2; i < COMP_CWORD; i++)); do
    word=${COMP_WORDS[i]}
    if (( skip )); then
      skip=0
      continue
    fi
    case "$word" in
      --title|--notes-file|--ca-file|--profile) skip=1 ;;
      -*) ;;
      *) ((positional += 1)) ;;
    esac
  done

  if (( positional == 0 && skip == 0 )); then
    case "$top:$sub" in
      profile:use|profile:remove)
        while IFS= read -r candidate; do
          [[ $candidate == "$cur"* ]] && COMPREPLY+=("$candidate")
        done < <("${COMP_WORDS[0]}" __complete profiles 2>/dev/null)
        return
        ;;
      script:show|script:update|script:edit|script:delete|note:show|note:set|note:edit|note:clear)
        state=all
        ;;
      script:restore|script:purge)
        state=trash
        ;;
    esac
    if [[ -n ${state-} ]]; then
      compopt -o filenames 2>/dev/null || true
      while IFS= read -r candidate; do
        [[ $candidate == "$cur"* ]] && COMPREPLY+=("$candidate")
      done < <("${COMP_WORDS[0]}" __complete scripts "$state" "$cur" 2>/dev/null)
      return
    fi
  fi

  case "$top:$sub:$positional" in
    script:create:0|script:update:1|note:set:1)
      compopt -o filenames
      while IFS= read -r candidate; do COMPREPLY+=("$candidate"); done < <(compgen -f -- "$cur")
      ;;
  esac
}
complete -F _bashido bashido
`

func (a *app) completionCommand(args []string) error {
	if hasHelp(args) {
		return a.printHelp("completion")
	}
	if len(args) != 1 {
		return fail(2, "usage: bashido completion bash|install")
	}
	if args[0] == "install" {
		return a.installBashCompletion()
	}
	if args[0] != "bash" {
		return fail(2, "usage: bashido completion bash|install")
	}
	_, err := fmt.Fprint(a.out, bashCompletion)
	return err
}

func (a *app) offerBashCompletion(dir string) error {
	interactive := a.isInteractive
	if interactive == nil {
		interactive = func() bool { return isTerminal(a.in) }
	}
	if !interactive() {
		return nil
	}
	latest, _, loadedDir, err := a.load()
	if err != nil {
		_, _ = a.warningf("Warning: could not check autocomplete preference: %s\n", sanitize(err.Error()))
		return nil
	}
	if loadedDir != dir {
		_, _ = a.warningf("Warning: configuration directory changed; autocomplete setup was skipped.\n")
		return nil
	}
	if latest.CompletionOffered {
		return nil
	}
	reader := bufio.NewReader(a.in)
	for {
		if _, err := fmt.Fprint(a.errOut, "Set up Bash tab completion? [y/N] "); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if errors.Is(err, io.EOF) && line == "" {
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			if saveErr := a.markCompletionOffered(dir); saveErr != nil {
				_, _ = a.warningf("Warning: could not save autocomplete preference: %s\n", sanitize(saveErr.Error()))
			}
			if installErr := a.installBashCompletion(); installErr != nil {
				_, _ = a.warningf("Warning: could not enable Bash completion: %s\nRun 'bashido completion install' to retry.\n", sanitize(installErr.Error()))
			}
			return nil
		case "", "n", "no":
			if saveErr := a.markCompletionOffered(dir); saveErr != nil {
				_, _ = a.warningf("Warning: could not save autocomplete preference: %s\n", sanitize(saveErr.Error()))
			}
			return nil
		default:
			if _, err = fmt.Fprintln(a.errOut, "Please answer y or n."); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func (a *app) markCompletionOffered(dir string) error {
	cfg, _, loadedDir, err := a.load()
	if err != nil {
		return err
	}
	if loadedDir != dir {
		return errors.New("configuration directory changed during login")
	}
	cfg.CompletionOffered = true
	return saveConfig(dir, cfg)
}

func isTerminal(stream any) bool {
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

func (a *app) installBashCompletion() error {
	home := a.getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	path := filepath.Join(home, ".bashrc")
	f, content, err := openBashRC(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) != bashCompletionSource {
			continue
		}
		_, err = fmt.Fprintf(a.out, "Bash completion is already configured in %s.\n", sanitize(path))
		return err
	}
	prefix := ""
	if len(content) > 0 && content[len(content)-1] != '\n' {
		prefix = "\n"
	}
	entry := prefix + "\n# Bashido tab completion\n" + bashCompletionSource + "\n"
	if _, err = io.WriteString(f, entry); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	_, err = a.successf("Enabled Bash completion in %s; open a new shell to use it.\n", sanitize(path))
	return err
}

func (a *app) removeBashCompletion() (string, bool, error) {
	home := a.getenv("HOME")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
	}
	path := filepath.Join(home, ".bashrc")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return path, false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return path, false, fmt.Errorf("refusing to modify non-regular file %s", path)
	}
	if info.Size() > maxBashRCSize {
		return path, false, fmt.Errorf("refusing to read %s: file exceeds size limit", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return path, false, fmt.Errorf("read %s: %w", path, err)
	}
	var kept []string
	changed := false
	for _, line := range strings.Split(string(content), "\n") {
		trim := strings.TrimSpace(line)
		if trim == bashCompletionSource || trim == "# Bashido tab completion" {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	if !changed {
		return path, false, nil
	}
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	next := []byte(strings.Join(kept, "\n"))
	if len(next) > 0 {
		next = append(next, '\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bashrc-")
	if err != nil {
		return path, false, err
	}
	tmpName := tmp.Name()
	if err = tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return path, false, err
	}
	if _, err = tmp.Write(next); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpName)
		return path, false, err
	}
	if err = os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return path, false, err
	}
	return path, true, nil
}

func openBashRC(path string) (*os.File, []byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		f, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0600)
		if createErr == nil {
			if lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); lockErr != nil {
				_ = f.Close()
				return nil, nil, fmt.Errorf("lock %s: %w", path, lockErr)
			}
			return f, nil, nil
		}
		if !errors.Is(createErr, os.ErrExist) {
			return nil, nil, fmt.Errorf("create %s: %w", path, createErr)
		}
		info, err = os.Lstat(path)
		if err != nil {
			return nil, nil, fmt.Errorf("inspect %s after create race: %w", path, err)
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("refusing to modify non-regular file %s", path)
	}
	if info.Size() > maxBashRCSize {
		return nil, nil, fmt.Errorf("refusing to read %s: file exceeds size limit", path)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("lock %s: %w", path, err)
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("refusing to modify changed file %s", path)
	}
	content, err := io.ReadAll(io.LimitReader(f, maxBashRCSize+1))
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(content) > maxBashRCSize {
		_ = f.Close()
		return nil, nil, fmt.Errorf("refusing to read %s: file exceeds size limit", path)
	}
	return f, content, nil
}

func (a *app) completionCandidates(ctx context.Context, args []string) error {
	if len(args) == 1 && args[0] == "profiles" {
		cfg, _, _, err := a.load()
		if err != nil {
			return err
		}
		rows := make([]string, 0, len(cfg.Profiles))
		for name := range cfg.Profiles {
			rows = append(rows, sanitize(name))
		}
		sort.Strings(rows)
		for _, row := range rows {
			if _, err = fmt.Fprintln(a.out, row); err != nil {
				return err
			}
		}
		return nil
	}
	if len(args) < 2 || len(args) > 3 || args[0] != "scripts" {
		return fail(2, "invalid completion request")
	}
	state := args[1]
	if state != "active" && state != "trash" && state != "all" {
		return fail(2, "invalid completion state")
	}
	cl, err := a.api()
	if err != nil {
		return err
	}
	var response scriptsEnvelope
	query := ""
	if len(args) == 3 {
		query = args[2]
	}
	path := "/api/v1/scripts?state=" + url.QueryEscape(state) + "&q=" + url.QueryEscape(query)
	requestCtx, cancel := context.WithTimeout(ctx, completionRequestTimeout)
	defer cancel()
	if _, err = cl.do(requestCtx, "GET", path, nil, &response, nil); err != nil {
		return err
	}
	titles := make(map[string]int, len(response.Scripts))
	for _, s := range response.Scripts {
		titles[s.Title]++
	}
	candidates := make(map[string]struct{}, len(response.Scripts)*2)
	for _, s := range response.Scripts {
		if s.ID != "" && sanitize(s.ID) == s.ID && strings.HasPrefix(s.ID, query) {
			candidates[s.ID] = struct{}{}
		}
		if s.Title != "" && titles[s.Title] == 1 && sanitize(s.Title) == s.Title && !strings.HasPrefix(s.Title, "-") && strings.HasPrefix(s.Title, query) {
			candidates[s.Title] = struct{}{}
		}
	}
	rows := make([]string, 0, len(candidates))
	for candidate := range candidates {
		rows = append(rows, candidate)
	}
	sort.Strings(rows)
	if len(rows) > maxCompletionCandidates {
		rows = rows[:maxCompletionCandidates]
	}
	for _, row := range rows {
		if _, err = fmt.Fprintln(a.out, row); err != nil {
			return err
		}
	}
	return nil
}
