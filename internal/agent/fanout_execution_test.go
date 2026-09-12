// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
)

type fanoutRequestClient struct {
	mu      sync.Mutex
	prompts []string
	metas   []llm.RequestMeta
	fail    string
}

func (c *fanoutRequestClient) CompletionsWithCtx(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	var prompt string
	for _, m := range req.Messages {
		if text, ok := m.Content.(string); ok {
			prompt += text
		}
	}
	meta, _ := llm.RequestMetaFromContext(ctx)
	c.mu.Lock()
	c.prompts = append(c.prompts, prompt)
	c.metas = append(c.metas, meta)
	c.mu.Unlock()
	if c.fail != "" && strings.Contains(prompt, c.fail) {
		return nil, errors.New("rule provider failed")
	}
	return agentTaskDoneResponse(), nil
}

func newFanoutExecutionAgent(t *testing.T, client llm.LLMClient) *Agent {
	t.Helper()
	a := newManifestFlowAgentWithClient(t, []model.Diff{{OldPath: "a.go", NewPath: "a.go", Diff: "+x", Insertions: 1}}, nil, client)
	a.args.FanOutProjectRules = true
	a.args.MaxConcurrency = 1
	a.args.Template.MainTask.Messages = []template.ChatMessage{{Role: "user", Content: "{{system_rule}} {{diffs}}"}}
	a.args.SystemRule = fanoutTestResolver{matches: map[string][]rules.RuleDetail{
		"a.go": {{Pattern: "*.go", Rule: "RULE_ONE"}, {Pattern: "*.go", Rule: "RULE_TWO"}},
	}}
	return a
}

func TestFanoutIndependentRequests(t *testing.T) {
	c := &fanoutRequestClient{}
	a := newFanoutExecutionAgent(t, c)
	if _, err := a.dispatchSubtasks(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(c.prompts) != 2 {
		t.Fatalf("requests = %d, want 2: %v", len(c.prompts), c.prompts)
	}
	if !strings.Contains(c.prompts[0], "RULE_ONE") || strings.Contains(c.prompts[0], "RULE_TWO") {
		t.Fatalf("first prompt mixed rules: %q", c.prompts[0])
	}
	if !strings.Contains(c.prompts[1], "RULE_TWO") || strings.Contains(c.prompts[1], "RULE_ONE") {
		t.Fatalf("second prompt mixed rules: %q", c.prompts[1])
	}
	if c.metas[0].FilePath == "" || c.metas[0].FilePath == c.metas[1].FilePath {
		t.Fatalf("request identities collided: %+v", c.metas)
	}
	m := finishManifestFlow(t, a)
	if len(m.Coverage.Completed) != 1 || len(m.Coverage.Failed) != 0 {
		t.Fatalf("successful fan-out coverage = %+v", m.Coverage)
	}
}

func TestFanoutPartialFailureNeverCheckpointsFile(t *testing.T) {
	for _, fail := range []string{"RULE_ONE", "RULE_TWO"} {
		t.Run(fail, func(t *testing.T) {
			c := &fanoutRequestClient{fail: fail}
			a := newFanoutExecutionAgent(t, c)
			_, _ = a.dispatchSubtasks(context.Background())
			m := finishManifestFlow(t, a)
			if len(c.prompts) != 2 {
				t.Fatalf("requests = %d, want 2: %v", len(c.prompts), c.prompts)
			}
			if len(m.Coverage.Completed) != 0 || len(m.Coverage.Failed) != 1 {
				t.Fatalf("partial rule coverage must fail the file: %+v", m.Coverage)
			}
			state, err := session.LoadReviewResumeState(a.args.RepoDir, a.session.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.Items) != 0 {
				t.Fatalf("partial file checkpointed: %+v", state.Items)
			}
		})
	}
}

func TestFanoutModeChangesResumeIdentity(t *testing.T) {
	a := New(Args{})
	before := a.ruleConfigSHA256()
	runtimeBefore := a.runtimeConfigSHA256()
	a.args.FanOutProjectRules = true
	if before == a.ruleConfigSHA256() {
		t.Error("fan-out must invalidate first-match checkpoints")
	}
	if runtimeBefore == a.runtimeConfigSHA256() {
		t.Error("runtime identity must expose fan-out")
	}
}
