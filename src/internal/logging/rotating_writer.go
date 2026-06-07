package logging

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type RotatingLineWriter struct {
	mu       sync.Mutex
	path     string
	maxLines int
	maxFiles int
	file     *os.File
	lines    int
}

func NewRotatingLineWriter(path string, maxLines, maxFiles int) (*RotatingLineWriter, error) {
	if maxLines <= 0 {
		maxLines = 2000
	}
	if maxFiles <= 0 {
		maxFiles = 5
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	lines, err := countLines(path)
	if err != nil {
		return nil, err
	}
	w := &RotatingLineWriter{
		path:     path,
		maxLines: maxLines,
		maxFiles: maxFiles,
		lines:    lines,
	}
	if w.lines >= w.maxLines {
		if err := w.rotateLocked(); err != nil {
			return nil, err
		}
		return w, nil
	}
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingLineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.openLocked(); err != nil {
			return 0, err
		}
	}
	if w.lines >= w.maxLines {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	if n > 0 {
		w.lines += lineCount(p[:n])
	}
	return n, err
}

func (w *RotatingLineWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingLineWriter) openLocked() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	w.file = file
	return nil
}

func (w *RotatingLineWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	if w.maxFiles <= 1 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		w.lines = 0
		return w.openLocked()
	}

	oldest := rotatedPath(w.path, w.maxFiles-1)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := w.maxFiles - 2; i >= 1; i-- {
		src := rotatedPath(w.path, i)
		dst := rotatedPath(w.path, i+1)
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(w.path, rotatedPath(w.path, 1)); err != nil && !os.IsNotExist(err) {
		return err
	}
	w.lines = 0
	return w.openLocked()
}

func rotatedPath(path string, index int) string {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return fmt.Sprintf("%s.%d%s", base, index, ext)
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func lineCount(p []byte) int {
	count := bytes.Count(p, []byte{'\n'})
	if count == 0 && len(p) > 0 {
		return 1
	}
	return count
}
