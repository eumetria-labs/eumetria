package ai

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ImageJob is one question about one image (a JPEG), answered in text.
type ImageJob struct {
	APIKey, BaseURL string
	Model, Effort   string
	System, Prompt  string
	JPEG            []byte
}

// AskImageAPI answers job on the Anthropic API in one request, without
// tools.
func AskImageAPI(ctx context.Context, job ImageJob) (*Answer, error) {
	opts := []option.RequestOption{option.WithAPIKey(job.APIKey)}
	if job.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(job.BaseURL))
	}
	client := anthropic.NewClient(opts...)
	resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:        anthropic.Model(job.Model),
		MaxTokens:    8000,
		System:       []anthropic.TextBlockParam{{Text: job.System}},
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(job.Effort)},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(
			anthropic.NewImageBlockBase64("image/jpeg", base64.StdEncoding.EncodeToString(job.JPEG)),
			anthropic.NewTextBlock(job.Prompt),
		)},
	}, FallbackOptions(job.Model)...)
	if err != nil {
		return nil, apiError(err)
	}
	ans := &Answer{Model: string(resp.Model), Usage: Usage{Input: resp.Usage.InputTokens, Output: resp.Usage.OutputTokens,
		CacheRead: resp.Usage.CacheReadInputTokens, CacheWrite: resp.Usage.CacheCreationInputTokens}}
	ans.Cost, ans.Known = Cost(ans.Model, ans.Usage)
	switch resp.StopReason {
	case anthropic.StopReasonRefusal:
		return ans, ErrRefused
	case anthropic.StopReasonMaxTokens:
		return ans, ErrTooLong
	}
	var text strings.Builder
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	ans.Text = strings.TrimSpace(text.String())
	return ans, nil
}
