// Package webdav 提供一个只读的 WebDAV 客户端，用于把远程书库同步到本地缓存。
package webdav

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Client 是绑定到一个 WebDAV 目录的只读客户端。
type Client struct {
	base *url.URL
	user string
	pass string
	http *http.Client
}

// File 是远程目录中的一个文件（不包含目录本身）。
type File struct {
	Path    string // 相对 WebDAV 根目录的路径，使用 / 分隔
	Name    string
	Size    int64
	ModTime time.Time
}

type davMultiStatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href     string        `xml:"href"`
	Propstat []davPropstat `xml:"propstat"`
}

type davPropstat struct {
	Prop   davProp `xml:"prop"`
	Status string  `xml:"status"`
}

type davProp struct {
	DisplayName   string          `xml:"displayname"`
	ContentLength string          `xml:"getcontentlength"`
	LastModified  string          `xml:"getlastmodified"`
	ResourceType  davResourceType `xml:"resourcetype"`
}

type davResourceType struct {
	Collection *davCollection `xml:"collection"`
}

type davCollection struct{}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>
<d:propfind xmlns:d="DAV:">
  <d:prop>
    <d:displayname/>
    <d:getcontentlength/>
    <d:getlastmodified/>
    <d:resourcetype/>
  </d:prop>
</d:propfind>`

// New 解析 WebDAV 根地址。地址必须是 http 或 https，末尾斜杠会自动补齐。
func New(rawURL, user, pass string) (*Client, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 地址无效：%w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("WebDAV 地址必须以 http:// 或 https:// 开头")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("WebDAV 地址缺少主机名")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return &Client{
		base: u,
		user: user,
		pass: pass,
		http: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// List 递归列出根目录下的所有文件。
func (c *Client) List(ctx context.Context) ([]File, error) {
	queue := []string{""}
	seen := map[string]bool{}
	files := make([]File, 0, 64)
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if seen[dir] {
			continue
		}
		seen[dir] = true
		entries, err := c.listDir(ctx, dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.dir {
				queue = append(queue, entry.file.Path)
				continue
			}
			files = append(files, entry.file)
		}
	}
	return files, nil
}

type davEntry struct {
	file File
	dir  bool
}

func (c *Client) listDir(ctx context.Context, dir string) ([]davEntry, error) {
	u := c.urlFor(dir)
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", u.String(), strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	c.auth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 WebDAV 失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WebDAV 返回 %s", resp.Status)
	}

	var ms davMultiStatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		return nil, fmt.Errorf("解析 WebDAV 目录失败：%w", err)
	}

	out := make([]davEntry, 0, len(ms.Responses))
	for _, item := range ms.Responses {
		rel, ok := c.relative(item.Href)
		if !ok || rel == dir {
			continue
		}
		prop := firstProp(item)
		if prop == nil {
			continue
		}
		isDir := prop.ResourceType.Collection != nil
		if rel == "" && isDir {
			continue
		}
		if rel == "" {
			continue
		}
		entry := davEntry{dir: isDir}
		entry.file.Path = rel
		entry.file.Name = strings.TrimSpace(prop.DisplayName)
		if entry.file.Name == "" {
			entry.file.Name = path.Base(rel)
		}
		entry.file.Size, _ = strconv.ParseInt(strings.TrimSpace(prop.ContentLength), 10, 64)
		if t, err := http.ParseTime(strings.TrimSpace(prop.LastModified)); err == nil {
			entry.file.ModTime = t
		}
		out = append(out, entry)
	}
	return out, nil
}

func firstProp(item davResponse) *davProp {
	for i := range item.Propstat {
		if strings.Contains(item.Propstat[i].Status, " 200 ") || item.Propstat[i].Status == "" {
			return &item.Propstat[i].Prop
		}
	}
	if len(item.Propstat) > 0 {
		return &item.Propstat[0].Prop
	}
	return nil
}

// relative 把 PROPFIND 返回的 href 转成相对根目录的路径。
func (c *Client) relative(href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	u := c.base.ResolveReference(ref)
	if !strings.EqualFold(u.Host, c.base.Host) {
		return "", false
	}
	basePath := strings.TrimSuffix(c.base.Path, "/")
	p := strings.TrimSuffix(u.Path, "/")
	if p == basePath {
		return "", true
	}
	if !strings.HasPrefix(p, basePath+"/") {
		return "", false
	}
	rel := strings.TrimPrefix(p, basePath+"/")
	rel = path.Clean(strings.Trim(rel, "/"))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

func (c *Client) urlFor(rel string) *url.URL {
	u := *c.base
	if rel != "" {
		u.Path = path.Join(c.base.Path, rel)
	}
	return &u
}

func (c *Client) auth(req *http.Request) {
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
}

// Download 把相对路径 rel 的文件下载到本地 dst。
func (c *Client) Download(ctx context.Context, rel, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.urlFor(rel).String(), nil)
	if err != nil {
		return err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 返回 %s", rel, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".webdav-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	ok = true
	return nil
}
