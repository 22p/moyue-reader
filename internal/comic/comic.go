// Package comic 提供 CBZ（ZIP）与 CBR（RAR）漫画包的页面枚举与读取。
package comic

import (
	"archive/zip"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// Page 是漫画包内的一页图片。
type Page struct {
	Name string
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".gif": true, ".bmp": true, ".avif": true,
}

// ErrEmpty 表示压缩包里没有图片。
var ErrEmpty = errors.New("压缩包中没有找到图片")

// ErrUnsupported 表示不是 CBZ/CBR。
var ErrUnsupported = errors.New("不支持的漫画格式")

// List 返回按自然顺序排序的图片页。
func List(path string) ([]Page, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cbz":
		return listZip(path)
	case ".cbr":
		return listRar(path)
	default:
		return nil, ErrUnsupported
	}
}

func listZip(path string) ([]Page, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	pages := make([]Page, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !usable(f.Name) {
			continue
		}
		pages = append(pages, Page{Name: f.Name})
	}
	if len(pages) == 0 {
		return nil, ErrEmpty
	}
	sort.Slice(pages, func(i, j int) bool { return naturalLess(pages[i].Name, pages[j].Name) })
	return pages, nil
}

func listRar(path string) ([]Page, error) {
	files, err := rardecode.List(path)
	if err != nil {
		return nil, err
	}
	pages := make([]Page, 0, len(files))
	for _, f := range files {
		if f.IsDir || !usable(f.Name) {
			continue
		}
		pages = append(pages, Page{Name: f.Name})
	}
	if len(pages) == 0 {
		return nil, ErrEmpty
	}
	sort.Slice(pages, func(i, j int) bool { return naturalLess(pages[i].Name, pages[j].Name) })
	return pages, nil
}

// Read 读取指定页的图片数据（上限 64MB）。
func Read(path, name string) ([]byte, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cbz":
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 64<<20))
			}
		}
		return nil, errors.New("页面不存在")
	case ".cbr":
		files, err := rardecode.List(path)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.Name == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 64<<20))
			}
		}
		return nil, errors.New("页面不存在")
	default:
		return nil, ErrUnsupported
	}
}

func usable(name string) bool {
	base := filepath.Base(name)
	if strings.HasPrefix(base, ".") || strings.HasPrefix(name, "__MACOSX/") {
		return false
	}
	return imageExts[strings.ToLower(filepath.Ext(base))]
}

// naturalLess 让 page2 排在 page10 前面。
func naturalLess(a, b string) bool {
	aa, bb := strings.ToLower(a), strings.ToLower(b)
	i, j := 0, 0
	for i < len(aa) && j < len(bb) {
		ca, cb := aa[i], bb[j]
		if isDigit(ca) && isDigit(cb) {
			ei, ej := i, j
			for ei < len(aa) && isDigit(aa[ei]) {
				ei++
			}
			for ej < len(bb) && isDigit(bb[ej]) {
				ej++
			}
			na := strings.TrimLeft(aa[i:ei], "0")
			nb := strings.TrimLeft(bb[j:ej], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			i, j = ei, ej
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	if (i < len(aa)) != (j < len(bb)) {
		return i < len(aa)
	}
	return a < b
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
