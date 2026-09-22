package agent

import (
	"strings"
	"testing"
)

func TestDefaultSystemPromptAnswersQuestionsAndPlansWithoutStartingWork(t *testing.T) {
	for _, instruction := range []string{
		"When the user asks a question, answer it and stop.",
		"A request for a plan or a plan review is not a request to implement it.",
	} {
		if !strings.Contains(DefaultSystemPrompt, instruction) {
			t.Errorf("default prompt is missing instruction %q", instruction)
		}
	}
}
