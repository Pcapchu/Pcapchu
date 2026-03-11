package investigation

import (
	"context"

	"github.com/Pcapchu/Pcapchu/internal/common"
	"github.com/cloudwego/eino/schema"
)

// LangHintModifier prepends a language instruction to the message list
// so that the LLM responds in the same language as the user query.
// Call SetQuery before the agent runs.
type LangHintModifier struct {
	hint string
}

// SetQuery detects the language of query and stores the appropriate hint.
func (m *LangHintModifier) SetQuery(query string) {
	lang := common.DetectLang(query)
	switch lang {
	case common.LangZH, common.LangMixed:
		m.hint = "请用中文回答所有问题和输出所有内容。"
	case common.LangEN:
		m.hint = "Please answer all questions and produce all output in English."
	default:
		m.hint = ""
	}
}

// Rewrite is a MessageRewriter-compatible function that prepends the
// language hint as a system message.
func (m *LangHintModifier) Rewrite(_ context.Context, msgs []*schema.Message) []*schema.Message {
	if m.hint == "" {
		return msgs
	}
	out := make([]*schema.Message, 0, len(msgs)+1)
	out = append(out, schema.SystemMessage(m.hint))
	out = append(out, msgs...)
	return out
}
