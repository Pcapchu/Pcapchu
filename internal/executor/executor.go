package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Pcapchu/Pcapchu/internal/common"
	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/prompts"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
	"strings"
	"sync"

	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// Graph node name constants.
const (
	nodeIsLast         = "is-last"
	nodeNormalPrepare  = "normal-prepare"
	nodeNormalTemplate = "normal-template"
	nodeNormalReact    = "normal-react"
	nodeNormalParse    = "normal-parse"
	nodeFinalPrepare   = "final-prepare"
	nodeFinalTemplate  = "final-template"
	nodeFinalReact     = "final-react"
	nodeFinalParse     = "final-parse"
)

// Result is the output of a full executor pipeline run.
type Result struct {
	Round        int
	Summary      string
	KeyFindings  string
	OpenQuestions string
	Findings     string
	OperationLog string
}

// Executor wraps a compiled eino graph that executes all plan steps.
type Executor struct {
	rAgent *react.Agent
	log    logger.Log
}

// NewExecutor creates a new Executor. The graph is built on each Run() call
// because it captures per-run closure state.
func NewExecutor(rAgent *react.Agent, log logger.Log) *Executor {
	return &Executor{rAgent: rAgent, log: log}
}

// Run executes all steps in the plan and returns the final report plus captured state.
// userQuery is the original user question, injected into executor prompts for context.
// pcapPath is the container-side path to the target PCAP file.
// round is the current investigation round number (1-based).
func (e *Executor) Run(ctx context.Context, plan common.Plan, userQuery string, pcapPath string, round int) (*Result, error) {
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("plan has no steps")
	}

	// --- Closure state for capturing findings/oplog after Invoke ---
	var (
		capturedFindings string
		capturedOpLog    []string
		captureMu        sync.Mutex
	)

	// --- Load prompt templates ---
	normalPrompt := prompts.MustGet("normal_executor")
	finalPrompt := prompts.MustGet("final_executor")

	// --- State initializer ---
	prepareStateFunc := func(ctx context.Context) *common.PlanState {
		tableSchema := plan.TableSchema
		if tableSchema == "" {
			tableSchema = "(Table schema not available - run `pcapchu-scripts meta` if needed)"
		}
		return &common.PlanState{
			Plan:             plan,
			TableSchema:      tableSchema,
			CurrentStepIndex: 0,
			ResearchFindings: "",
			OperationLog:     []string{},
			EndOutput:        "",
		}
	}

	// --- ReAct wrapper with logging (callback is injected at graph level, not here) ---
	reactWithLog := func(label string) *compose.Lambda {
		return compose.InvokableLambda(func(ctx context.Context, in []*schema.Message) (*schema.Message, error) {
			e.log.Info(ctx, label+" input", logger.A(logger.AttrMessageCount, len(in)))
			out, err := e.rAgent.Generate(ctx, in)
			if err != nil {
				e.log.Error(ctx, label+" error", logger.A(logger.AttrError, err.Error()))
				return nil, err
			}
			e.log.Info(ctx, label+" output",
				logger.A("content", common.TruncateStr(out.Content, 500)))
			return out, nil
		})
	}

	// ===================== NORMAL EXECUTOR NODES =====================

	// normal-prepare: inject template variables from PlanState
	normalPrepareLambda := compose.InvokableLambda(func(ctx context.Context, in any) (map[string]any, error) {
		return nil, nil
	})
	normalPreparePostHook := func(ctx context.Context, out map[string]any, state *common.PlanState) (map[string]any, error) {
		idx := state.CurrentStepIndex
		if idx >= len(state.Plan.Steps) {
			return nil, fmt.Errorf("step index %d out of range (total %d)", idx, len(state.Plan.Steps))
		}
		step := state.Plan.Steps[idx]
		e.log.Info(ctx, "executor step start", logger.A(logger.AttrStepID, step.StepID), logger.A("intent", step.Intent))

		e.log.Emit(events.TypeStepStarted, events.StepStartedData{
			StepID:     step.StepID,
			Intent:     step.Intent,
			TotalSteps: len(state.Plan.Steps),
		})

		planOverview := common.FormatPlanOverview(state.Plan)
		opLog := strings.Join(state.OperationLog, "\n---\n")
		if opLog == "" {
			opLog = "(No operations performed yet - you are the first executor)"
		}
		findings := state.ResearchFindings
		if findings == "" {
			findings = "(No research findings yet - you are the first executor)"
		}

		return map[string]any{
			"user_query":        userQuery,
			"pcap_path":         pcapPath,
			"plan_overview":     planOverview,
			"research_findings": findings,
			"operation_log":     opLog,
			"current_step":      fmt.Sprintf("Step %d: %s", step.StepID, step.Intent),
			"table_schema":      state.TableSchema,
		}, nil
	}

	// normal-template
	normalTpl := prompt.FromMessages(schema.GoTemplate,
		schema.SystemMessage(normalPrompt),
		schema.UserMessage("Execute your assigned step now. Current step: {{.current_step}}"),
	)

	// normal-parse: extract JSON, update state, capture for Result
	normalParseLambda := compose.InvokableLambda(func(ctx context.Context, in *schema.Message) (any, error) {
		return nil, nil
	})
	normalParsePreHook := func(ctx context.Context, in *schema.Message, state *common.PlanState) (*schema.Message, error) {
		idx := state.CurrentStepIndex
		step := state.Plan.Steps[idx]

		str, err := common.ExtractJSON(in.Content)
		if err != nil {
			e.log.Emit(events.TypeStepError, events.ErrorData{
				Phase:   "executor",
				Message: fmt.Sprintf("extract json failed for step %d: %v", step.StepID, err),
				StepID:  step.StepID,
			})
			return nil, fmt.Errorf("extract JSON from executor output: %w", err)
		}

		var parsed common.NormalOutput
		if err := json.Unmarshal([]byte(str), &parsed); err != nil {
			e.log.Emit(events.TypeStepError, events.ErrorData{
				Phase:   "executor",
				Message: fmt.Sprintf("unmarshal failed for step %d: %v", step.StepID, err),
				StepID:  step.StepID,
			})
			return nil, fmt.Errorf("unmarshal executor output: %w", err)
		}

		// Append findings
		if parsed.Findings.String() != "" {
			state.ResearchFindings += fmt.Sprintf("\n\n### Step %d: %s\n%s", step.StepID, step.Intent, parsed.Findings)
		}

		// Append operation log
		if parsed.MyActions.String() != "" {
			state.OperationLog = append(state.OperationLog, fmt.Sprintf("[Step %d - %s]\n%s", step.StepID, step.Intent, parsed.MyActions))
		}

		// Emit step findings event
		e.log.Emit(events.TypeStepFindings, events.StepFindingsData{
			StepID:   step.StepID,
			Intent:   step.Intent,
			Findings: parsed.Findings.String(),
			Actions:  parsed.MyActions.String(),
		})

		// --- Capture state via closure for Result ---
		captureMu.Lock()
		capturedFindings = state.ResearchFindings
		capturedOpLog = make([]string, len(state.OperationLog))
		copy(capturedOpLog, state.OperationLog)
		captureMu.Unlock()

		// Advance step index
		state.CurrentStepIndex++
		e.log.Info(ctx, "executor step completed",
			logger.A(logger.AttrStepID, step.StepID),
			logger.A("next_index", state.CurrentStepIndex))
		return nil, nil
	}

	// ===================== FINAL EXECUTOR NODES =====================

	// final-prepare: inject template variables for the final step
	finalPrepareLambda := compose.InvokableLambda(func(ctx context.Context, in any) (map[string]any, error) {
		return nil, nil
	})
	finalPreparePostHook := func(ctx context.Context, out map[string]any, state *common.PlanState) (map[string]any, error) {
		idx := state.CurrentStepIndex
		if idx >= len(state.Plan.Steps) {
			return nil, fmt.Errorf("step index %d out of range in FinalExecutor (total %d)", idx, len(state.Plan.Steps))
		}
		step := state.Plan.Steps[idx]
		e.log.Info(ctx, "executor final step start", logger.A(logger.AttrStepID, step.StepID), logger.A("intent", step.Intent))

		e.log.Emit(events.TypeStepStarted, events.StepStartedData{
			StepID:     step.StepID,
			Intent:     step.Intent,
			TotalSteps: len(state.Plan.Steps),
		})

		planOverview := common.FormatPlanOverview(state.Plan)
		opLog := strings.Join(state.OperationLog, "\n---\n")
		if opLog == "" {
			opLog = "(No operations recorded)"
		}
		findings := state.ResearchFindings
		if findings == "" {
			findings = "(No research findings accumulated)"
		}

		return map[string]any{
			"user_query":        userQuery,
			"pcap_path":         pcapPath,
			"plan_overview":     planOverview,
			"research_findings": findings,
			"operation_log":     opLog,
			"table_schema":      state.TableSchema,
		}, nil
	}

	// final-template
	finalTpl := prompt.FromMessages(schema.GoTemplate,
		schema.SystemMessage(finalPrompt),
		schema.UserMessage("Synthesize all findings into a phased round summary now."),
	)

	// final-parse: extract JSON round summary from final executor output
	finalParseLambda := compose.InvokableLambda(func(ctx context.Context, in *schema.Message) (string, error) {
		e.log.Info(ctx, "final executor output",
			logger.A(logger.AttrContentLength, len(in.Content)),
			logger.A("content", common.TruncateStr(in.Content, 1000)))

		str, err := common.ExtractJSON(in.Content)
		if err != nil {
			// Fallback: wrap raw content as summary JSON
			fallback, _ := json.Marshal(common.RoundSummary{
				Summary: common.FlexString(in.Content),
			})
			return string(fallback), nil
		}

		// Validate it parses as RoundSummary
		var rs common.RoundSummary
		if err := json.Unmarshal([]byte(str), &rs); err != nil {
			fallback, _ := json.Marshal(common.RoundSummary{
				Summary: common.FlexString(in.Content),
			})
			return string(fallback), nil
		}

		return str, nil
	})

	// ===================== IS-LAST NODE =====================

	isLastLambda := compose.InvokableLambda(func(ctx context.Context, in any) (bool, error) {
		return false, nil
	})
	isLastPostHook := func(ctx context.Context, out bool, state *common.PlanState) (bool, error) {
		remaining := len(state.Plan.Steps) - state.CurrentStepIndex
		isLast := remaining == 1
		e.log.Info(ctx, "executor loop check",
			logger.A("current_index", state.CurrentStepIndex),
			logger.A("remaining", remaining),
			logger.A("is_last", isLast))
		return isLast, nil
	}

	// ===================== BUILD GRAPH =====================

	g := compose.NewGraph[any, string](compose.WithGenLocalState(prepareStateFunc))

	// Add nodes
	_ = g.AddLambdaNode(nodeIsLast, isLastLambda, compose.WithStatePostHandler(isLastPostHook))

	_ = g.AddLambdaNode(nodeNormalPrepare, normalPrepareLambda, compose.WithStatePostHandler(normalPreparePostHook))
	_ = g.AddChatTemplateNode(nodeNormalTemplate, normalTpl)
	_ = g.AddLambdaNode(nodeNormalReact, reactWithLog("ReAct-NormalExecutor"))
	_ = g.AddLambdaNode(nodeNormalParse, normalParseLambda, compose.WithStatePreHandler(normalParsePreHook))

	_ = g.AddLambdaNode(nodeFinalPrepare, finalPrepareLambda, compose.WithStatePostHandler(finalPreparePostHook))
	_ = g.AddChatTemplateNode(nodeFinalTemplate, finalTpl)
	_ = g.AddLambdaNode(nodeFinalReact, reactWithLog("ReAct-FinalExecutor"))
	_ = g.AddLambdaNode(nodeFinalParse, finalParseLambda)

	// Branch: is-last decides normal vs final path
	condition := func(ctx context.Context, in bool) (string, error) {
		if in {
			return nodeFinalPrepare, nil
		}
		return nodeNormalPrepare, nil
	}
	branch := compose.NewGraphBranch(condition, map[string]bool{
		nodeNormalPrepare: true,
		nodeFinalPrepare:  true,
	})

	// Edges
	_ = g.AddEdge(compose.START, nodeIsLast)
	_ = g.AddBranch(nodeIsLast, branch)

	_ = g.AddEdge(nodeNormalPrepare, nodeNormalTemplate)
	_ = g.AddEdge(nodeNormalTemplate, nodeNormalReact)
	_ = g.AddEdge(nodeNormalReact, nodeNormalParse)
	_ = g.AddEdge(nodeNormalParse, nodeIsLast) // loop back

	_ = g.AddEdge(nodeFinalPrepare, nodeFinalTemplate)
	_ = g.AddEdge(nodeFinalTemplate, nodeFinalReact)
	_ = g.AddEdge(nodeFinalReact, nodeFinalParse)
	_ = g.AddEdge(nodeFinalParse, compose.END)

	// Compile with generous step limit (5 nodes per loop × max 20 steps)
	compiled, err := g.Compile(ctx, compose.WithMaxRunSteps(100))
	if err != nil {
		return nil, fmt.Errorf("compile executor graph: %w", err)
	}

	// ===================== INVOKE =====================

	report, err := compiled.Invoke(ctx, struct{}{})
	if err != nil {
		e.log.Emit(events.TypeError, events.ErrorData{
			Phase:   "executor",
			Message: err.Error(),
		})
		return nil, fmt.Errorf("executor invoke: %w", err)
	}

	// Parse the JSON round summary
	var roundSummary common.RoundSummary
	if err := json.Unmarshal([]byte(report), &roundSummary); err != nil {
		roundSummary = common.RoundSummary{Summary: common.FlexString(report)}
	}

	// Emit report event
	e.log.Emit(events.TypeReportGenerated, events.ReportData{
		Round:      round,
		Report:     roundSummary.Summary.String(),
		ContentLen: len(report),
		TotalSteps: len(plan.Steps),
	})

	// Build result from captured closure state
	captureMu.Lock()
	result := &Result{
		Round:        round,
		Summary:      roundSummary.Summary.String(),
		KeyFindings:  roundSummary.KeyFindings.String(),
		OpenQuestions: roundSummary.OpenQuestions.String(),
		Findings:     capturedFindings,
		OperationLog: strings.Join(capturedOpLog, "\n---\n"),
	}
	captureMu.Unlock()

	e.log.Info(ctx, "executor completed",
		logger.A("report_length", len(report)))
	return result, nil
}
