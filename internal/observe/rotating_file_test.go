package observe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotatingFileDoesNotRotateAtBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "agent.log")
	writer, err := OpenRotatingFile(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("cd")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	assertFileText(t, path, "abcd")
	assertMissing(t, path+".1")
}

func TestRotatingFileCrossingPreservesOneBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	writer, err := OpenRotatingFile(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, "abc")
	writeRecord(t, writer, "de")
	assertFileText(t, path+".1", "abc")
	assertFileText(t, path, "de")

	writeRecord(t, writer, "fgh")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	assertFileText(t, path+".1", "de")
	assertFileText(t, path, "fgh")
	assertMissing(t, path+".2")
}

func TestRotatingFileReopenHonorsExistingSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte("full"), 0o640); err != nil {
		t.Fatal(err)
	}
	writer, err := OpenRotatingFile(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, "x")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	assertFileText(t, path+".1", "full")
	assertFileText(t, path, "x")
}

func TestRotatingFileConcurrentRecordsRemainWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	writer, err := OpenRotatingFile(path, 24)
	if err != nil {
		t.Fatal(err)
	}
	const records = 40
	var wg sync.WaitGroup
	for i := 0; i < records; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, _ = writer.Write([]byte(fmt.Sprintf("R%02d\n", index)))
		}(i)
	}
	wg.Wait()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{path + ".1", path} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if len(line) != 3 || line[0] != 'R' {
				t.Fatalf("split or malformed record %q in %s", line, candidate)
			}
		}
	}
	assertMissing(t, path+".2")
}

func TestRotatingFileRejectsInvalidConfiguration(t *testing.T) {
	if _, err := OpenRotatingFile("", 1); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := OpenRotatingFile(filepath.Join(t.TempDir(), "agent.log"), 0); err == nil {
		t.Fatal("zero limit accepted")
	}
	if _, err := OpenRotatingFile(filepath.Join(t.TempDir(), "agent.log"), -1); err == nil {
		t.Fatal("negative limit accepted")
	}
}

func TestRotatingFileRotationFailureIsTerminal(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.log")
	writer, err := OpenRotatingFile(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, "abc")
	backupDirectory := path + ".1"
	if err := os.Mkdir(backupDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDirectory, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := writer.Write([]byte("de")); err == nil {
		t.Fatal("rotation failure was not returned")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("later")); err == nil {
		t.Fatal("write after terminal rotation failure succeeded")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("active file grew after terminal failure: %d -> %d", before.Size(), after.Size())
	}
}

func writeRecord(t *testing.T, writer *RotatingFile, text string) {
	t.Helper()
	if n, err := writer.Write([]byte(text)); err != nil || n != len(text) {
		t.Fatalf("Write(%q) = (%d, %v)", text, n, err)
	}
}

func assertFileText(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or stat failed with %v", path, err)
	}
}
