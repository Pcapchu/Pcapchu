package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Pcapchu/Pcapchu/internal/common"
	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/prompts"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"strings"

	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// Graph node name constants.
const (
	nodePlannerPrompt = "planner-prompt"
	nodePlannerReact  = "planner-react"
	nodePlannerParse  = "planner-parse"
)

// PlannerInput is the input to a planner invocation.
type PlannerInput struct {
	UserQuery string
	PcapPath  string                 // container-side path to the target PCAP
	History   *common.SessionHistory // nil on first round
}

// Planner wraps a compiled eino graph that produces a Plan from a user query.
type Planner struct {
	graph compose.Runnable[map[string]any, common.Plan]
	log   logger.Log
}

// NewPlanner builds the planner graph: prompt → react → parse.
func NewPlanner(ctx context.Context, rAgent *react.Agent, log logger.Log) (*Planner, error) {
	tpl := prompt.FromMessages(schema.GoTemplate,
		schema.SystemMessage(prompts.MustGet("planner")),
		schema.UserMessage("{{.user_input}}"),
	)

	// Planner ReAct — callback is injected via compose.WithCallbacks at graph Compile/Invoke level,
	// not created manually here.
	plannerReActLambda := compose.InvokableLambda(func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
		log.Info(ctx, "planner react input", logger.A(logger.AttrMessageCount, len(in)))
		out, err := rAgent.Generate(ctx, in)
		if err != nil {
			log.Error(ctx, "planner react error", logger.A(logger.AttrError, err.Error()))
			return nil, err
		}
		log.Info(ctx, "planner react output",
			logger.A("content", common.TruncateStr(out.Content, 500)))
		return out, nil
	})

	// Parse LLM output → Plan
	parseLambda := compose.InvokableLambda(func(ctx context.Context, input *schema.Message) (common.Plan, error) {
		var plan common.Plan
		str, err := common.ExtractJSON(input.Content)
		if err != nil {
			return common.Plan{}, fmt.Errorf("extract json from planner output: %w", err)
		}
		log.Info(ctx, "planner extracted json", logger.A("json", common.TruncateStr(str, 500)))
		if err := json.Unmarshal([]byte(str), &plan); err != nil {
			return common.Plan{}, fmt.Errorf("unmarshal plan: %w | content: %s", err, common.TruncateStr(input.Content, 1000))
		}
		log.Info(ctx, "planner plan parsed", logger.A("step_count", len(plan.Steps)))
		return plan, nil
	})

	g := compose.NewGraph[map[string]any, common.Plan]()
	_ = g.AddChatTemplateNode(nodePlannerPrompt, tpl)
	_ = g.AddLambdaNode(nodePlannerReact, plannerReActLambda)
	_ = g.AddLambdaNode(nodePlannerParse, parseLambda)
	_ = g.AddEdge(compose.START, nodePlannerPrompt)
	_ = g.AddEdge(nodePlannerPrompt, nodePlannerReact)
	_ = g.AddEdge(nodePlannerReact, nodePlannerParse)
	_ = g.AddEdge(nodePlannerParse, compose.END)

	compiled, err := g.Compile(ctx)
	if err != nil {
		return nil, fmt.Errorf("compile planner graph: %w", err)
	}

	return &Planner{graph: compiled, log: log}, nil
}

// Run executes the planner graph and returns a Plan.
// If input.History is non-nil, session history is injected as a user message before the query.
func (p *Planner) Run(ctx context.Context, input PlannerInput) (common.Plan, error) {
	templateVars := map[string]any{
		"user_input": input.UserQuery,
		"pcap_path":  input.PcapPath,
	}

	// If we have session history, prepend it to the user input
	if input.History != nil {
		historySection := buildHistorySection(input.History)
		if historySection != "" {
			templateVars["user_input"] = historySection + "\n\n---\n\n**Current Query:**\n" + input.UserQuery
		}
	}

	plan, err := p.graph.Invoke(ctx, templateVars)
	if err != nil {
		p.log.Emit(events.TypePlanError, events.ErrorData{
			Phase:   "planner",
			Message: err.Error(),
		})
		return common.Plan{}, fmt.Errorf("planner invoke: %w", err)
	}

	// Emit plan created event
	steps := make([]events.StepInfo, len(plan.Steps))
	for i, s := range plan.Steps {
		steps[i] = events.StepInfo{StepID: s.StepID, Intent: s.Intent}
	}
	p.log.Emit(events.TypePlanCreated, events.PlanCreatedData{
		Thought:    plan.Thought,
		TotalSteps: len(plan.Steps),
		Steps:      steps,
	})

	return plan, nil
}

// buildHistorySection formats session history into a markdown section for the planner prompt.
func buildHistorySection(h *common.SessionHistory) string {
	if h == nil {
		return ""
	}

	var parts []string

	if h.Findings != "" {
		parts = append(parts, "## Previous Research Findings\n\n"+h.Findings)
	}
	if h.OperationLog != "" {
		parts = append(parts, "## Previous Operation Log\n\n"+h.OperationLog)
	}
	if h.PreviousReport != nil {
		parts = append(parts, fmt.Sprintf("## Most Recent Round Summary (Round %d)\n\n**Summary:** %s\n\n**Key Findings:** %s\n\n**Open Questions:** %s",
			h.PreviousReport.Round, h.PreviousReport.Summary, h.PreviousReport.KeyFindings, h.PreviousReport.OpenQuestions))
	}
	if len(h.AllReports) > 1 {
		var sb strings.Builder
		sb.WriteString("## All Round Summaries\n\n")
		for _, r := range h.AllReports {
			sb.WriteString(fmt.Sprintf("### Round %d\n**Key Findings:** %s\n\n", r.Round, r.KeyFindings))
		}
		parts = append(parts, sb.String())
	}

	if len(parts) == 0 {
		return ""
	}

	return "# Context From Previous Rounds\n\n" + strings.Join(parts, "\n\n---\n\n")
}
