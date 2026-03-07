package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino-ext/components/tool/commandline"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

var (
	bashToolInfo = &schema.ToolInfo{
		Name: "bash",
		Desc: `Run commands in a bash shell
* When invoking this tool, the contents of the \"command\" parameter does NOT need to be XML-escaped.
* You don't have access to the internet via this tool.
* State is persistent across command calls and discussions with the user.
* To inspect a particular line range of a file, e.g. lines 10-25, try 'sed -n 10,25p /path/to/the/file'.
* Please avoid commands that may produce a very large amount of output.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type:     "string",
				Desc:     "The command to execute",
				Required: true,
			},
		}),
	}
)

func NewBashTool(op commandline.Operator) tool.InvokableTool {
	return &bashTool{op: op}
}

type bashTool struct {
	op commandline.Operator
}

func (b *bashTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return bashToolInfo, nil
}

type shellInput struct {
	Command string `json:"command"`
}

func (b *bashTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	input := &shellInput{}
	err := json.Unmarshal([]byte(argumentsInJSON), input)
	if err != nil {
		return "", err
	}
	if len(input.Command) == 0 {
		return "command cannot be empty", nil
	}
	o := tool.GetImplSpecificOptions(&options{b.op}, opts...)
	cmd, err := o.op.RunCommand(ctx, []string{"bash", "-c", input.Command})
	if err != nil {
		if strings.HasPrefix(err.Error(), "internal error") {
			return err.Error(), nil
		}
		return "", err
	}
	return formatCommandOutput(cmd), nil
}

type options struct {
	op commandline.Operator
}

func formatCommandOutput(output *commandline.CommandOutput) string {
	return fmt.Sprintf("---\nstdout:%v\n---\nstderr:%v\n---", output.Stdout, output.Stderr)
}
