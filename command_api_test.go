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
			json.NewEncoder(w).Encode(scriptEnvelope{Script: script{ID: id, Title: "New", Content: raw, Revision: 8}})
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
	if out.String() != id+"\n" {
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
	}{
		{name: "quit unchanged", editor: "#!/bin/sh\nexit 0\n", wantContent: content},
		{name: "save change", editor: "#!/bin/sh\nprintf '#!/bin/sh\\necho new\\n' > \"$1\"\n", wantContent: "#!/bin/sh\necho new\n", wantPatch: true},
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

			a, _, _ := testApp(t)
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
		})
	}
}
