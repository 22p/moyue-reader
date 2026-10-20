package epub

import (
	"archive/zip"
	"io"
	"strings"
)

// Archive 提供对 EPUB 内部资源的只读访问。
type Archive struct {
	zr    *zip.ReadCloser
	files map[string]*zip.File
}

// OpenArchive 打开 EPUB 压缩包，使用完毕后需要调用 Close。
func OpenArchive(file string) (*Archive, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	a := &Archive{zr: zr, files: make(map[string]*zip.File, len(zr.File))}
	for _, f := range zr.File {
		name := normalize(f.Name)
		if name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		a.files[name] = f
	}
	return a, nil
}

// Read 读取内部文件内容（大小上限 32MB）。
func (a *Archive) Read(name string) ([]byte, error) {
	name = normalize(name)
	f, ok := a.files[name]
	if !ok {
		for key, candidate := range a.files {
			if strings.EqualFold(key, name) {
				f = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, ErrInvalid
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 32<<20))
}

// Close 关闭底层压缩包。
func (a *Archive) Close() error {
	return a.zr.Close()
}
