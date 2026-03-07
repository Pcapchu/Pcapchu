package common

import (
	"fmt"
	"regexp"
	"strings"
)

// thinkTagRe matches <think>...</think> blocks (including multiline).
var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// ExtractJSON strips <think> blocks, markdown code fences, and locates the first JSON object in the input.
func ExtractJSON(input string) (string, error) {
	// Strip <think>...</think> reasoning blocks (e.g., DeepSeek models)
	input = thinkTagRe.ReplaceAllString(input, "")
	input = strings.TrimSpace(input)
	if strings.Contains(input, "```") {
		start := strings.Index(input, "```")
		end := strings.LastIndex(input, "```")
		if end > start {
			content := input[start+3 : end]
			if idx := strings.Index(content, "\n"); idx != -1 {
				if strings.Contains(strings.ToLower(content[:idx]), "json") {
					content = content[idx+1:]
				}
			}
			input = content
		}
	}

	start := strings.Index(input, "{")
	end := strings.LastIndex(input, "}")

	if start == -1 || end == -1 || start > end {
		return "", fmt.Errorf("no valid json object found")
	}

	return input[start : end+1], nil
}

// TruncateStr truncates a string to maxLen characters, appending "..." if truncated.
func TruncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// FormatPlanOverview formats a Plan into a human-readable markdown summary.
func FormatPlanOverview(p Plan) string {
	var sb strings.Builder
	sb.WriteString("## Investigation Plan\n\n")
	sb.WriteString("**Planner Thought**: " + p.Thought + "\n\n")
	for _, s := range p.Steps {
		sb.WriteString(fmt.Sprintf("- **Step %d**: %s\n", s.StepID, s.Intent))
	}
	return sb.String()
}
