package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// decompressAndWrite extracts the evva binary from a compressed asset and
// writes it to a temp file. Returns the path to the extracted binary.
func decompressAndWrite(name string, data []byte) (string, error) {
	var (
		bin  []byte
		err  error
	)

	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		bin, err = extractTarGz(data)
	case strings.HasSuffix(name, ".zip"):
		bin, err = extractZip(data)
	default:
		return "", fmt.Errorf("unknown archive format: %s", name)
	}
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "evva-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmp.Close()

	if _, err := tmp.Write(bin); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("write temp file: %w", err)
	}

	if err := tmp.Chmod(0o755); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("chmod temp file: %w", err)
	}

	return tmp.Name(), nil
}

func extractTarGz(data []byte) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar reader: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && (hdr.Name == "evva" || hdr.Name == "evva.exe") {
			bin, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read tar entry: %w", err)
			}
			return bin, nil
		}
	}
	return nil, fmt.Errorf("evva binary not found in tar.gz archive")
}

func extractZip(data []byte) ([]byte, error) {
	// zip.NewReader needs the data as an io.ReaderAt with known size.
	// We write to a temp file to satisfy that contract.
	tmp, err := os.CreateTemp("", "evva-update-zip-*")
	if err != nil {
		return nil, fmt.Errorf("create zip temp: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		return nil, fmt.Errorf("write zip temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("close zip temp: %w", err)
	}

	fi, err := os.Stat(tmp.Name())
	if err != nil {
		return nil, fmt.Errorf("stat zip temp: %w", err)
	}

	zr, err := zip.NewReader(tmp, fi.Size())
	if err != nil {
		return nil, fmt.Errorf("zip reader: %w", err)
	}

	for _, f := range zr.File {
		if f.Name == "evva" || f.Name == "evva.exe" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry: %w", err)
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("evva binary not found in zip archive")
}

// replaceBinary atomically replaces dst with src on Unix. On Windows it
// cannot overwrite a running executable, so we write a helper script and
// tell the user to run it.
func replaceBinary(dst, src string) error {
	// On Unix we can do an atomic rename as long as src and dst are on the
	// same filesystem. os.Rename is atomic on Linux/macOS within the same
	// mount point, which the temp dir and the binary dir almost always are.
	if err := os.Rename(src, dst); err != nil {
		// If rename fails (cross-device), fall back to copy+remove.
		if isCrossDevice(err) {
			return copyAndRemove(src, dst)
		}
		return fmt.Errorf("rename %s → %s: %w", src, dst, err)
	}
	return nil
}

func isCrossDevice(err error) bool {
	return strings.Contains(err.Error(), "cross-device") ||
		strings.Contains(err.Error(), "invalid cross-device link")
}

func copyAndRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	// Write to a temp file in the same dir as dst, then rename.
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".evva-tmp-*")
	if err != nil {
		return fmt.Errorf("create dst tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}

	si, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat src: %w", err)
	}
	if err := os.Chmod(tmpPath, si.Mode()); err != nil {
		return fmt.Errorf("chmod tmp: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("rename tmp → dst: %w", err)
	}
	return os.Remove(src)
}
