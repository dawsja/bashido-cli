package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func configureServer(t *testing.T, a *app, origin string) {
	t.Helper()
	cfg, creds, dir, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Current = "p"
	cfg.Profiles["p"] = profile{Origin: origin}
	creds.Profiles["p"] = credential{Origin: origin, Token: "secret"}
	if err = saveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if err = saveCredentials(dir, creds); err != nil {
		t.Fatal(err)
	}
}

func TestShowRawAndUpdateRevision(t *testing.T) {
	const id = "abcdef1234567890"
	const raw = "#!/bin/sh\nprintf 'no newline'"
	patched := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("authorization missing")
		}
		switch {
		case r.URL.Path == "/api/v1/scripts":
			json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: []script{{ID: id, Title: "Deploy", Revision: 7}}})
		case r.URL.Path == "/api/v1/scripts/"+id && r.Method == "GET":
			json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "Deploy", Content: raw, Revision: 7}})
		case r.URL.Path == "/api/v1/scripts/"+id && r.Method == "PATCH":
			patched = true
			if r.Header.Get("If-Match") != "7" {
				t.Errorf("If-Match = %q", r.Header.Get("If-Match"))
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "Canonical New", Content: raw, Revision: 8}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(404)
			io.WriteString(w, `{"error":{"code":"not_found","message":"missing"}}`)
		}
	}))
	defer ts.Close()
	a, out, _ := testApp(t)
	configureServer(t, a, ts.URL)
	if err := a.run(t.Context(), []string{"script", "show", "Deploy"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != raw {
		t.Fatalf("raw output = %q", out.String())
	}
	out.Reset()
	path := t.TempDir() + "/content"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.run(t.Context(), []string{"script", "update", "Deploy", path, "--title", "New"}); err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("PATCH not sent")
	}
	if out.String() != "Updated script \"Canonical New\" ("+id+").\n" {
		t.Fatalf("update output = %q", out.String())
	}
}

func TestEditScript(t *testing.T) {
	const id = "abcdef1234567890"
	const content = "#!/bin/sh\necho old\n"

	for _, tc := range []struct {
		name        string
		editor      string
		wantContent string
		wantPatch   bool
		wantOutput  string
	}{
		{name: "quit unchanged", editor: "#!/bin/sh\nexit 0\n", wantContent: content, wantOutput: "No changes to script \"Deploy\" (abcdef1234567890).\n"},
		{name: "save change", editor: "#!/bin/sh\nprintf '#!/bin/sh\\necho new\\n' > \"$1\"\n", wantContent: "#!/bin/sh\necho new\n", wantPatch: true, wantOutput: "Updated script \"Deploy\" (abcdef1234567890).\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patched := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/scripts":
					json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: []script{{ID: id, Title: "Deploy", Revision: 7}}})
				case r.URL.Path == "/api/v1/scripts/"+id && r.Method == "GET":
					json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "Deploy", Content: content, Revision: 7}})
				case r.URL.Path == "/api/v1/scripts/"+id && r.Method == "PATCH":
					patched = true
					if r.Header.Get("If-Match") != "7" {
						t.Errorf("If-Match = %q", r.Header.Get("If-Match"))
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["content"] != tc.wantContent {
						t.Errorf("content = %q", body["content"])
					}
					json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Content: body["content"], Revision: 8}})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.String())
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			a, out, _ := testApp(t)
			configureServer(t, a, ts.URL)
			dir := t.TempDir()
			editor := filepath.Join(dir, "editor")
			if err := os.WriteFile(editor, []byte(tc.editor), 0700); err != nil {
				t.Fatal(err)
			}
			getenv := a.getenv
			a.getenv = func(key string) string {
				if key == "BASHIDO_EDITOR" {
					return editor
				}
				if key == "XDG_RUNTIME_DIR" {
					return dir
				}
				return getenv(key)
			}

			if err := a.run(t.Context(), []string{"script", "edit", "Deploy"}); err != nil {
				t.Fatal(err)
			}
			if patched != tc.wantPatch {
				t.Errorf("patched = %v, want %v", patched, tc.wantPatch)
			}
			if out.String() != tc.wantOutput {
				t.Errorf("output = %q, want %q", out.String(), tc.wantOutput)
			}
		})
	}
}

func TestScriptMutationAcknowledgements(t *testing.T) {
	const id = "abcdef1234567890"
	for _, tc := range []struct {
		command string
		method  string
		path    string
		want    string
	}{
		{command: "delete", method: "DELETE", path: "/api/v1/scripts/" + id, want: "Deleted script \"Test\" (" + id + ").\n"},
		{command: "restore", method: "POST", path: "/api/v1/scripts/" + id + "/restore", want: "Restored script \"Test\" (" + id + ").\n"},
		{command: "purge", method: "DELETE", path: "/api/v1/scripts/" + id + "/permanent", want: "Permanently deleted script \"Test\" (" + id + ").\n"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			mutated := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/scripts":
					json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: []script{{ID: id, Title: "Test", Revision: 1}}})
				case r.URL.Path == "/api/v1/scripts/"+id && r.Method == "GET":
					json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "Test", Revision: 1}})
				case r.URL.Path == tc.path && r.Method == tc.method:
					mutated = true
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.String())
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			a, out, _ := testApp(t)
			configureServer(t, a, ts.URL)
			args := []string{"script", tc.command, "Test"}
			if tc.command == "purge" {
				args = append(args, "--yes")
			}
			if err := a.run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			if !mutated {
				t.Fatal("mutation request not sent")
			}
			if out.String() != tc.want {
				t.Fatalf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestCreateScriptAcknowledgement(t *testing.T) {
	const id = "abcdef1234567890"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scripts" || r.Method != "POST" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["title"] != "Test" || body["content"] != "echo test\n" {
			t.Errorf("body = %#v", body)
		}
		json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "Canonical Test"}})
	}))
	defer ts.Close()

	a, out, _ := testApp(t)
	configureServer(t, a, ts.URL)
	path := filepath.Join(t.TempDir(), "test.sh")
	if err := os.WriteFile(path, []byte("echo test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.run(t.Context(), []string{"script", "create", path, "--title", "Test"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Created script \"Canonical Test\" ("+id+").\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestNoteMutationAcknowledgements(t *testing.T) {
	const id = "abcdef1234567890"
	for _, tc := range []struct {
		name         string
		command      string
		method       string
		editor       string
		wantMutation bool
		wantRevision string
		wantOutput   string
	}{
		{name: "set", command: "set", method: "PUT", wantMutation: true, wantRevision: "7", wantOutput: "Updated notes for script \"Deploy\" (" + id + ").\n"},
		{name: "edit unchanged", command: "edit", editor: "#!/bin/sh\nexit 0\n", wantOutput: "Notes unchanged for script \"Deploy\" (" + id + ").\n"},
		{name: "edit changed", command: "edit", method: "PUT", editor: "#!/bin/sh\nprintf 'new notes' > \"$1\"\n", wantMutation: true, wantRevision: "9", wantOutput: "Updated notes for script \"Deploy\" (" + id + ").\n"},
		{name: "clear", command: "clear", method: "DELETE", wantMutation: true, wantRevision: "7", wantOutput: "Cleared notes for script \"Deploy\" (" + id + ").\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/scripts":
					json.NewEncoder(w).Encode(scriptsEnvelope{Scripts: []script{{ID: id, Title: "Deploy", Revision: 7}}})
				case r.URL.Path == "/api/v1/scripts/"+id && r.Method == "GET":
					json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "Deploy", Revision: 7}})
				case r.URL.Path == "/api/v1/scripts/"+id+"/notes" && r.Method == "GET":
					json.NewEncoder(w).Encode(note{Notes: "old notes", Revision: 9})
				case r.URL.Path == "/api/v1/scripts/"+id+"/notes" && r.Method == tc.method:
					mutated = true
					if got := r.Header.Get("If-Match"); got != tc.wantRevision {
						t.Errorf("If-Match = %q, want %q", got, tc.wantRevision)
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.String())
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()

			a, out, _ := testApp(t)
			configureServer(t, a, ts.URL)
			var args []string
			switch tc.command {
			case "set":
				path := filepath.Join(t.TempDir(), "notes")
				if err := os.WriteFile(path, []byte("new notes"), 0600); err != nil {
					t.Fatal(err)
				}
				args = []string{"note", "set", "Deploy", path}
			case "edit":
				dir := t.TempDir()
				editor := filepath.Join(dir, "editor")
				if err := os.WriteFile(editor, []byte(tc.editor), 0700); err != nil {
					t.Fatal(err)
				}
				getenv := a.getenv
				a.getenv = func(key string) string {
					if key == "BASHIDO_EDITOR" {
						return editor
					}
					if key == "XDG_RUNTIME_DIR" {
						return dir
					}
					return getenv(key)
				}
				args = []string{"note", "edit", "Deploy"}
			case "clear":
				args = []string{"note", "clear", "Deploy", "--yes"}
			}
			if err := a.run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			if mutated != tc.wantMutation {
				t.Errorf("mutated = %v, want %v", mutated, tc.wantMutation)
			}
			if got := out.String(); got != tc.wantOutput {
				t.Fatalf("output = %q, want %q", got, tc.wantOutput)
			}
		})
	}
}

func TestLogoutAcknowledgements(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "remote"
		if local {
			name = "local only"
		}
		t.Run(name, func(t *testing.T) {
			revoked := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/me/credential" && r.Method == "DELETE" {
					revoked = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				t.Errorf("unexpected %s %s", r.Method, r.URL.String())
				w.WriteHeader(http.StatusNotFound)
			}))
			defer ts.Close()

			a, out, errOut := testApp(t)
			configureServer(t, a, ts.URL)
			args := []string{"auth", "logout"}
			if local {
				args = append(args, "--local-only")
			}
			if err := a.run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			if revoked == local {
				t.Fatalf("revoked = %v, local = %v", revoked, local)
			}
			if local {
				if got := out.String(); got != "Removed the local credential for profile \"p\".\n" {
					t.Fatalf("output = %q", got)
				}
				if got := errOut.String(); got != "Warning: the server credential was not revoked.\n" {
					t.Fatalf("stderr = %q", got)
				}
			} else if got := out.String(); got != "Logged out of profile \"p\" and revoked its credential.\n" {
				t.Fatalf("output = %q", got)
			}
			out.Reset()
			if err := a.run(t.Context(), []string{"auth", "logout"}); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != "Already logged out of profile \"p\".\n" {
				t.Fatalf("no-op output = %q", got)
			}
		})
	}

	t.Run("mismatched credential origin", func(t *testing.T) {
		requests := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(http.StatusNoContent)
		}))
		defer ts.Close()

		a, out, errOut := testApp(t)
		configureServer(t, a, ts.URL)
		_, creds, dir, err := a.load()
		if err != nil {
			t.Fatal(err)
		}
		creds.Profiles["p"] = credential{Origin: "https://other.example", Token: "secret"}
		if err = saveCredentials(dir, creds); err != nil {
			t.Fatal(err)
		}
		if err = a.run(t.Context(), []string{"auth", "logout"}); err != nil {
			t.Fatal(err)
		}
		if requests != 0 {
			t.Fatalf("sent mismatched credential in %d request(s)", requests)
		}
		if got := out.String(); got != "Removed the local credential for profile \"p\".\n" {
			t.Fatalf("output = %q", got)
		}
		if got := errOut.String(); got != "Warning: the server credential was not revoked.\n" {
			t.Fatalf("stderr = %q", got)
		}
	})
}

func TestProfileRemoveAcknowledgements(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "remote"
		if local {
			name = "local only"
		}
		t.Run(name, func(t *testing.T) {
			revoked := false
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/me/credential" && r.Method == "DELETE" {
					revoked = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				t.Errorf("unexpected %s %s", r.Method, r.URL.String())
				w.WriteHeader(http.StatusNotFound)
			}))
			defer ts.Close()

			a, out, errOut := testApp(t)
			configureServer(t, a, ts.URL)
			cfg, _, dir, err := a.load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Profiles["z"] = profile{Origin: "https://example.com"}
			if err = saveConfig(dir, cfg); err != nil {
				t.Fatal(err)
			}
			args := []string{"profile", "remove", "p", "--yes"}
			if local {
				args = append(args, "--local-only")
			}
			if err = a.run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			if revoked == local {
				t.Fatalf("revoked = %v, local = %v", revoked, local)
			}
			want := "Removed profile \"p\" and revoked its credential.\nCurrent profile is now \"z\".\n"
			if local {
				want = "Removed profile \"p\" and its local credential.\nCurrent profile is now \"z\".\n"
				if got := errOut.String(); got != "Warning: the server credential was not revoked.\n" {
					t.Fatalf("stderr = %q", got)
				}
			}
			if got := out.String(); got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}
