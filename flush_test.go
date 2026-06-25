package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestResourceAttrsCarriesOSType(t *testing.T) {
	got := map[string]string{}
	for _, kv := range resourceAttrs("1.2.3", "windows", "mid-1") {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["os.type"] != "windows" {
		t.Fatalf("os.type = %q, want windows", got["os.type"])
	}
	if got["service.name"] != "ai-agent-telemetry" {
		t.Fatalf("service.name = %q", got["service.name"])
	}
	if got["service.version"] != "1.2.3" {
		t.Fatalf("service.version = %q", got["service.version"])
	}
	if got["machine.id"] != "mid-1" {
		t.Fatalf("machine.id = %q", got["machine.id"])
	}

	// An empty machine id is omitted, not sent blank.
	for _, kv := range resourceAttrs("v", "linux", "") {
		if string(kv.Key) == "machine.id" {
			t.Fatal("machine.id must be omitted when empty")
		}
	}
}

func seed(t *testing.T, s *Outbox, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Enqueue(SkillEvent{Agent: "codex", Skill: "s", TS: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFlushSendsAndClearsOnSuccess(t *testing.T) {
	isolateConfigCache(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 3)

	sent, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 3 {
		t.Fatalf("sent = %d, want 3", sent)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("collector received no requests")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("outbox not cleared: %d files remain", len(files))
	}
}

func TestFlushTrustsConfiguredCA(t *testing.T) {
	isolateConfigCache(t)
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	cfg := &tls.Config{RootCAs: pool}

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 2)

	sent, err := Flush(s, srv.URL, "", cfg, 2*time.Second)
	if err != nil {
		t.Fatalf("flush over TLS with configured CA: %v", err)
	}
	if sent != 2 {
		t.Fatalf("sent = %d, want 2", sent)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("collector received no requests")
	}
}

func TestFlushFailsUntrustedTLS(t *testing.T) {
	isolateConfigCache(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)

	// nil tlsConfig => system trust store, which does not trust the test cert.
	_, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err == nil {
		t.Fatal("want TLS verification error without the CA")
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer should be intact on TLS failure: %d files", len(files))
	}
}

func TestFlushKeepsBufferOnServerError(t *testing.T) {
	isolateConfigCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 2)

	_, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err == nil {
		t.Fatal("want error on server 500")
	}
	files, _ := s.List()
	if len(files) != 2 {
		t.Fatalf("buffer should be intact: %d files remain, want 2", len(files))
	}
}

func TestFlushEmptyEndpointIsNoop(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)
	sent, err := Flush(s, "", "", nil, time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer changed: %d files", len(files))
	}
}

func TestFlushSkipsWhenLocked(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)
	// Hold the lock from this test.
	release, err := lockOutbox(s)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer release()

	sent, err := Flush(s, "http://127.0.0.1:0", "", nil, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0 (should skip when locked)", sent)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer changed while locked: %d files", len(files))
	}
}
