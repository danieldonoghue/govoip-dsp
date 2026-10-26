package testutil

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// SpeechDir is where internal/testutil/fetch-speech.sh puts LibriSpeech
// utterances, relative to the repository root.
const SpeechDir = ".cache/speech/librispeech"

// SpeechRate is the sampling rate of the files in SpeechDir.
const SpeechRate = 16000

// RepoRoot returns the directory holding go.mod, searching upwards from the
// working directory.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// SpeechFiles returns the sorted paths of the downloaded 16 kHz int16 raw
// speech files, and skips the test when there are none.
func SpeechFiles(t testing.TB) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(RepoRoot(t), SpeechDir, "*.raw"))
	if err != nil || len(paths) == 0 {
		t.Skipf("no speech files in %s; run `make speech` to download LibriSpeech dev-clean", SpeechDir)
	}
	sort.Strings(paths)
	return paths
}

// LoadSpeech reads one speech file.
func LoadSpeech(t testing.TB, path string) []int16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return DecodeLE[int16](data)
}
