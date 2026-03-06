package summarizer

import (
	"errors"
)

var (
	// ErrConfigNil is returned when the config is nil.
	ErrConfigNil = errors.New("config is nil")

	// ErrModelRequired is returned when the model is not provided in config.
	ErrModelRequired = errors.New("model is required in config")

	// ErrTokenCountMismatch is returned when token counts don't match message count.
	ErrTokenCountMismatch = errors.New("token count mismatch with message count")
)

const summaryMessageFlag = "_agent_middleware_summary_message"
