package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var acks = []string{
	"Understood.", "Acknowledged.", "Very well.", "Affirmative.",
	"I see.", "Noted.", "Processing.", "Of course.",
}

const (
	piperVoiceModel  = "en_US-danny-low.onnx"
	piperVoiceConfig = "en_US-danny-low.onnx.json"
	piperVoiceURL    = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/en_US/danny/low/" + piperVoiceModel
	piperConfigURL   = "https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/en/en_US/danny/low/" + piperVoiceConfig
	piperReleaseBase = "https://github.com/rhasspy/piper/releases/download/2023.11.14-2/"
	phonemizeBase    = "https://github.com/rhasspy/piper-phonemize/releases/download/2023.11.14-4/"
)

type halVoice struct {
	mu        sync.Mutex
	ackIndex  int
	voiceDir  string // ~/.config/hal9000/voice
	piperPath string // path to piper binary
	libDir    string // path to dylibs
	modelPath string // path to .onnx model
	ready     bool   // piper is available
	setupOnce sync.Once
}

func newHALVoice() *halVoice {
	home, _ := os.UserHomeDir()
	voiceDir := filepath.Join(home, ".config", "hal9000", "voice")
	v := &halVoice{voiceDir: voiceDir}
	// Kick off setup in background so it doesn't block startup
	go v.ensureSetup()
	return v
}

func (v *halVoice) ensureSetup() {
	v.setupOnce.Do(func() {
		piperBin := filepath.Join(v.voiceDir, "piper", "piper")
		libDir := filepath.Join(v.voiceDir, "piper-phonemize", "lib")
		modelFile := filepath.Join(v.voiceDir, piperVoiceModel)
		configFile := filepath.Join(v.voiceDir, piperVoiceConfig)

		// Check if everything is already in place
		if fileExists(piperBin) && fileExists(modelFile) && fileExists(configFile) && dirHasDylibs(libDir) {
			v.piperPath = piperBin
			v.libDir = libDir
			v.modelPath = modelFile
			v.ready = true
			return
		}

		// Create voice directory
		os.MkdirAll(v.voiceDir, 0755)

		arch := "aarch64"
		if runtime.GOARCH == "amd64" {
			arch = "x64"
		}

		// Download piper binary if needed
		if !fileExists(piperBin) {
			tarURL := piperReleaseBase + fmt.Sprintf("piper_macos_%s.tar.gz", arch)
			if err := downloadAndExtractTar(tarURL, v.voiceDir); err != nil {
				return
			}
			os.Chmod(piperBin, 0755)
		}

		// Download piper-phonemize (shared libraries) if needed
		if !dirHasDylibs(libDir) {
			tarURL := phonemizeBase + fmt.Sprintf("piper-phonemize_macos_%s.tar.gz", arch)
			if err := downloadAndExtractTar(tarURL, v.voiceDir); err != nil {
				return
			}
		}

		// Download voice model if needed
		if !fileExists(modelFile) {
			if err := downloadFile(piperVoiceURL, modelFile); err != nil {
				return
			}
		}

		// Download voice config if needed
		if !fileExists(configFile) {
			if err := downloadFile(piperConfigURL, configFile); err != nil {
				return
			}
		}

		if fileExists(piperBin) && fileExists(modelFile) && dirHasDylibs(libDir) {
			v.piperPath = piperBin
			v.libDir = libDir
			v.modelPath = modelFile
			v.ready = true
		}
	})
}

func (v *halVoice) sayAsync(text string) {
	go func() {
		v.mu.Lock()
		defer v.mu.Unlock()

		// Try piper first
		if v.ready {
			wavFile := filepath.Join(os.TempDir(), "hal9000_voice.wav")
			cmd := exec.Command(v.piperPath, "--model", v.modelPath, "--output_file", wavFile)
			cmd.Stdin = strings.NewReader(text)
			// Set DYLD_LIBRARY_PATH so piper can find its shared libraries
			cmd.Env = append(os.Environ(), "DYLD_LIBRARY_PATH="+v.libDir)
			if cmd.Run() == nil {
				exec.Command("afplay", wavFile).Run()
				os.Remove(wavFile)
				return
			}
		}

		// Fallback to macOS say
		if runtime.GOOS == "darwin" {
			cmd := exec.Command("say", "-r", "220", "-v", "Daniel", text)
			if cmd.Run() != nil {
				exec.Command("say", "-r", "220", text).Run()
			}
		}
	}()
}

func (v *halVoice) sayShort(text string) {
	if len(text) < 100 {
		v.sayAsync(text)
	} else {
		v.acknowledge()
	}
}

func (v *halVoice) acknowledge() {
	v.mu.Lock()
	phrase := acks[v.ackIndex%len(acks)]
	v.ackIndex++
	v.mu.Unlock()
	v.sayAsync(phrase)
}

// ── Download helpers ────────────────────────────────────────

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirHasDylibs(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".dylib") {
			return true
		}
	}
	return false
}

func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func downloadAndExtractTar(url, destDir string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)
		// Prevent path traversal
		if !strings.HasPrefix(target, destDir) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			io.Copy(f, tr)
			f.Close()
			if hdr.Mode&0111 != 0 {
				os.Chmod(target, 0755)
			}
		}
	}
	return nil
}
