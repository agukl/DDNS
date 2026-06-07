package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingLineWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ddns.log")
	writer, err := NewRotatingLineWriter(path, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	for i := 0; i < 7; i++ {
		if _, err := fmt.Fprintf(writer, "line %d\n", i); err != nil {
			t.Fatal(err)
		}
	}

	files := []string{
		path,
		rotatedPath(path, 1),
		rotatedPath(path, 2),
	}
	for _, file := range files {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("expected %s to exist: %v", file, err)
		}
	}
	if _, err := os.Stat(rotatedPath(path, 3)); !os.IsNotExist(err) {
		t.Fatalf("oldest rotated file should not exist, err=%v", err)
	}
}
