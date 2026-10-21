package webdav

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListAndDownload(t *testing.T) {
	mux := http.NewServeMux()
	propfind := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			t.Fatalf("method = %s", r.Method)
		}
		if r.Header.Get("Depth") != "1" {
			t.Fatalf("depth = %s", r.Header.Get("Depth"))
		}
		if user, pass, ok := r.BasicAuth(); !ok || user != "alice" || pass != "secret" {
			t.Fatalf("basic auth = %q %q %v", user, pass, ok)
		}
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(propfindXML(r.URL.Path)))
	}
	mux.HandleFunc("/dav/", propfind)
	mux.HandleFunc("/dav/sub/", propfind)
	mux.HandleFunc("/dav/a.epub", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("epub-bytes"))
	})
	mux.HandleFunc("/dav/sub/b.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("text-bytes"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(srv.URL+"/dav", "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	files, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if f, ok := byPath["a.epub"]; !ok || f.Size != 10 || f.ModTime.IsZero() {
		t.Fatalf("a.epub = %+v", f)
	}
	if f, ok := byPath["sub/b.txt"]; !ok || f.Size != 10 {
		t.Fatalf("sub/b.txt = %+v", f)
	}

	dst := filepath.Join(t.TempDir(), "sub", "b.txt")
	if err := c.Download(context.Background(), "sub/b.txt", dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "text-bytes" {
		t.Fatalf("download = %q", data)
	}

	limited := filepath.Join(t.TempDir(), "limited.txt")
	if err := c.Download(context.Background(), "sub/b.txt", limited, 2); err == nil {
		t.Fatal("oversized download succeeded")
	}
	if _, err := os.Stat(limited); !os.IsNotExist(err) {
		t.Fatalf("oversized download left file: %v", err)
	}
}

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com/dav", "https:///dav"} {
		if _, err := New(raw, "", ""); err == nil {
			t.Errorf("New(%q) succeeded", raw)
		}
	}
}

func propfindXML(dir string) string {
	type item struct {
		Href       string
		Name       string
		Collection bool
		Size       int
		Mod        string
	}
	items := []item{{Href: dir, Name: "dav", Collection: true}}
	if dir == "/dav/" {
		items = append(items,
			item{Href: "/dav/a.epub", Name: "a.epub", Size: 10, Mod: "Wed, 21 Oct 2026 07:28:00 GMT"},
			item{Href: "/dav/sub/", Name: "sub", Collection: true},
		)
	} else {
		items = append(items, item{Href: "/dav/sub/b.txt", Name: "b.txt", Size: 10, Mod: "Wed, 21 Oct 2026 07:28:00 GMT"})
	}
	type response struct {
		XMLName  xml.Name `xml:"response"`
		Href     string   `xml:"href"`
		Propstat struct {
			Prop struct {
				DisplayName   string `xml:"displayname"`
				ContentLength int    `xml:"getcontentlength"`
				LastModified  string `xml:"getlastmodified"`
				ResourceType  struct {
					Collection *struct{} `xml:"collection,omitempty"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
			Status string `xml:"status"`
		} `xml:"propstat"`
	}
	ms := struct {
		XMLName  xml.Name   `xml:"multistatus"`
		Xmlns    string     `xml:"xmlns:d,attr"`
		Response []response `xml:"response"`
	}{Xmlns: "DAV:"}
	for _, it := range items {
		var r response
		r.Href = it.Href
		r.Propstat.Prop.DisplayName = it.Name
		r.Propstat.Prop.ContentLength = it.Size
		r.Propstat.Prop.LastModified = it.Mod
		if it.Collection {
			r.Propstat.Prop.ResourceType.Collection = &struct{}{}
		}
		r.Propstat.Status = "HTTP/1.1 200 OK"
		ms.Response = append(ms.Response, r)
	}
	out, _ := xml.Marshal(ms)
	return xml.Header + string(out)
}
