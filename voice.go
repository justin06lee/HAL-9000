package main

import (
	"os/exec"
	"runtime"
	"sync"
)

var acks = []string{
	"Understood.", "Acknowledged.", "Very well.", "Affirmative.",
	"I see.", "Noted.", "Processing.", "Of course.",
}

type halVoice struct {
	mu       sync.Mutex
	ackIndex int
}

func newHALVoice() *halVoice {
	return &halVoice{}
}

func (v *halVoice) sayAsync(text string) {
	go func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if runtime.GOOS != "darwin" {
			return
		}
		// Daniel (British male) is the closest to HAL's calm, deliberate tone.
		// Rate 110 = slow and measured, like the original HAL 9000.
		for _, voice := range []string{"Daniel", "Fred", "Ralph", "Albert"} {
			cmd := exec.Command("say", "-r", "110", "-v", voice, text)
			if cmd.Run() == nil {
				return
			}
		}
		// Fallback: default voice
		exec.Command("say", "-r", "110", text).Run()
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
