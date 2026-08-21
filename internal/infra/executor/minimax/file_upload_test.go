package minimax

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtFromURL(t *testing.T) {
	cases := map[string]string{
		"https://example.test/a/b/photo.png": ".png",
		// A query string after the extension is included verbatim — the
		// naive LastIndex has no idea "?" starts a query string. Documents
		// the real (imperfect) behavior rather than an assumption about it.
		"https://example.test/a/b/photo.png?x=1": ".png?x=1",
		"https://example.test/noext":             ".jpg",
		"https://example.test/a.b/noext":         ".jpg", // dot is in a path segment before the last "/", not the filename
	}
	for in, want := range cases {
		if got := extFromURL(in); got != want {
			t.Errorf("extFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUploadOrGetCachedHit(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("UploadFile must never be called on a cache hit")
	})
	defer closeFn()

	cache := newFakeFileCache()
	cache.byID["asset-1"] = "cached-file-id"

	fileID, cached, err := uploadOrGetCached(context.Background(), client, newFakeReader(), cache, "asset-1", defaultUploadPurpose)
	if err != nil {
		t.Fatalf("uploadOrGetCached: %v", err)
	}
	if !cached {
		t.Error("cached = false, want true")
	}
	if fileID != "cached-file-id" {
		t.Errorf("fileID = %q, want cached-file-id", fileID)
	}
}

func TestUploadOrGetCachedMiss(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/asset.png", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("asset-bytes"))
	})
	var gotPurpose string
	mux.HandleFunc("/v1/files/upload", func(w http.ResponseWriter, req *http.Request) {
		gotPurpose = req.FormValue("purpose")
		w.Write([]byte(`{"file":{"file_id":55},"base_resp":{"status_code":0}}`))
	})
	client := NewClient(srv.URL, "test-api-key")

	reader := newFakeReader()
	reader.set("asset-2", srv.URL+"/asset.png")
	cache := newFakeFileCache()

	fileID, cached, err := uploadOrGetCached(context.Background(), client, reader, cache, "asset-2", "video_generation_input")
	if err != nil {
		t.Fatalf("uploadOrGetCached: %v", err)
	}
	if cached {
		t.Error("cached = true, want false on a first-time upload")
	}
	if fileID != "55" {
		t.Errorf("fileID = %q, want 55", fileID)
	}
	if gotPurpose != "video_generation_input" {
		t.Errorf("purpose = %q, want video_generation_input", gotPurpose)
	}
	// The result must now be cached so a second call never re-uploads.
	if id, ok, _ := cache.Get(context.Background(), "asset-2"); !ok || id != "55" {
		t.Errorf("cache after upload = (%q, %v), want (55, true)", id, ok)
	}
	if cache.calls != 1 {
		t.Errorf("cache.Put called %d times, want exactly 1", cache.calls)
	}
}

func TestUploadOrGetCachedAssetNotFound(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		t.Fatal("UploadFile must never be called when the asset can't be resolved")
	})
	defer closeFn()

	_, _, err := uploadOrGetCached(context.Background(), client, newFakeReader(), newFakeFileCache(), "does-not-exist", defaultUploadPurpose)
	if err == nil {
		t.Fatal("expected an error when reader.PublicURL fails")
	}
}

func TestUploadOrGetCachedUploadBusinessError(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/asset.png", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("bytes"))
	})
	mux.HandleFunc("/v1/files/upload", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"bad key"}}`))
	})
	client := NewClient(srv.URL, "test-api-key")
	reader := newFakeReader()
	reader.set("asset-3", srv.URL+"/asset.png")
	cache := newFakeFileCache()

	if _, _, err := uploadOrGetCached(context.Background(), client, reader, cache, "asset-3", defaultUploadPurpose); err == nil {
		t.Fatal("expected an error when MiniMax's upload response carries a business error")
	}
	if cache.calls != 0 {
		t.Errorf("cache.Put called %d times, want 0 (nothing should be cached on failure)", cache.calls)
	}
}

func TestFileUploadPluginExecute(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/asset.png", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte("bytes"))
	})
	mux.HandleFunc("/v1/files/upload", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"file":{"file_id":77},"base_resp":{"status_code":0}}`))
	})
	client := NewClient(srv.URL, "test-api-key")
	reader := newFakeReader()
	reader.set("asset-4", srv.URL+"/asset.png")

	plugin := NewFileUploadPlugin(client, reader, newFakeFileCache())
	req := execRequest(t, "task-1", map[string]any{"asset-id": "asset-4"})
	out, err := plugin.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := outputValue[string](t, out, "file-id"); got != "77" {
		t.Errorf("file-id = %q, want 77", got)
	}
	if got := outputValue[bool](t, out, "cached"); got {
		t.Error("cached = true, want false on first upload")
	}
}
