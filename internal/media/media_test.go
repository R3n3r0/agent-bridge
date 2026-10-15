package media

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	pngData  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	jpegData = append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 32)...)
	webpData = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
)

func dataURL(mt string, b []byte) string {
	return "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func TestDataURLs(t *testing.T) {
	r := NewHTTPResolver(Options{MaxBytes: 100})
	ctx := context.Background()
	for _, c := range []struct {
		data []byte
		mt   string
	}{{pngData, "image/png"}, {jpegData, "image/jpeg"}, {[]byte("GIF89a" + strings.Repeat("x", 20)), "image/gif"}, {webpData, "image/webp"}} {
		img, err := r.Resolve(ctx, dataURL("image/whatever", c.data))
		if err != nil || img.MediaType != c.mt || len(img.Data) != len(c.data) {
			t.Fatalf("%s: %+v %v", c.mt, img, err)
		}
	}
	raw := "data:image/png;base64," + base64.RawStdEncoding.EncodeToString(pngData[:10])
	if _, err := r.Resolve(ctx, raw); err != nil {
		t.Fatalf("unpadded base64: %v", err)
	}
	bad := []string{
		"data:image/png,notbase64",
		"data:image/png;base64,!!!",
		"data:image/png;base64",
		dataURL("image/png", []byte("plain text, not an image at all")),
		dataURL("image/png", append(pngData, make([]byte, 200)...)),
		"ftp://example.com/a.png",
		"https://example.com/a.png", // URLs disabled
	}
	for _, u := range bad {
		if _, err := r.Resolve(ctx, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%.40s: got %v", u, err)
		}
	}
}

func TestDownload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(pngData) })
	mux.HandleFunc("/big.png", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(append(pngData, make([]byte, 500)...)) })
	mux.HandleFunc("/missing.png", http.NotFound)
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ctx := context.Background()

	// The test server is on loopback: blocked by default.
	strict := NewHTTPResolver(Options{MaxBytes: 100, FetchURLs: true})
	if _, err := strict.Resolve(ctx, ts.URL+"/img.png"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("loopback fetch allowed: %v", err)
	}

	open := NewHTTPResolver(Options{MaxBytes: 100, FetchURLs: true, AllowPrivateNetworks: true})
	img, err := open.Resolve(ctx, ts.URL+"/img.png")
	if err != nil || img.MediaType != "image/png" {
		t.Fatalf("download: %+v %v", img, err)
	}
	for _, path := range []string{"/big.png", "/missing.png", "/redirect"} {
		if _, err := open.Resolve(ctx, ts.URL+path); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", path, err)
		}
	}
	if _, err := open.Resolve(ctx, "http://127.0.0.1:1/x.png"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestPublicIP(t *testing.T) {
	for _, c := range []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true}, {"2001:4860:4860::8888", true},
		{"127.0.0.1", false}, {"::1", false}, {"10.1.2.3", false}, {"192.168.1.1", false}, {"172.16.0.1", false},
		{"169.254.169.254", false}, {"fe80::1", false}, {"fc00::1", false}, {"0.0.0.0", false},
		{"100.64.0.1", false}, {"224.0.0.1", false}, {"255.255.255.255", false},
	} {
		if got := publicIP(net.ParseIP(c.ip)); got != c.public {
			t.Errorf("%s: public=%v", c.ip, got)
		}
	}
}
