// Package cliexec implements the gateway's fixed GitHub CLI capabilities.
package cliexec

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/registry"
)

const (
	maxNumber = 2147483647
	maxBody   = 65536
	maxLimit  = 50
)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)

type capability struct {
	action, description, fields string
	states                      []string
	number, body, merge         bool
}

var capabilities = []capability{
	{action: "pr.list", description: "List pull requests", fields: "number,title,state,url", states: []string{"open", "closed", "merged", "all"}},
	{action: "pr.view", description: "Read a pull request", fields: "number,title,body,state,url", number: true},
	{action: "issue.list", description: "List issues", fields: "number,title,state,url", states: []string{"open", "closed", "all"}},
	{action: "issue.view", description: "Read an issue", fields: "number,title,body,state,url", number: true},
	{action: "repo.view", description: "Read repository metadata", fields: "nameWithOwner,description,url,isPrivate"},
	{action: "issue.comment", description: "Comment on an issue", number: true, body: true},
	{action: "pr.comment", description: "Comment on a pull request", number: true, body: true},
	{action: "pr.merge", description: "Merge a pull request", number: true, merge: true},
}

var mergeMethods = []string{"squash", "merge", "rebase"}

func denied() error {
	return fault.WithCode(fault.New(fault.Invalid, "GitHub CLI command or arguments are outside the allowlist", nil), "cli_command_denied")
}

func findCapability(action string) (capability, bool) {
	for _, c := range capabilities {
		if c.action == action {
			return c, true
		}
	}
	return capability{}, false
}

func validRepo(repo string) bool {
	if !repoPattern.MatchString(repo) {
		return false
	}
	_, name, _ := strings.Cut(repo, "/")
	return name != "." && name != ".."
}

func decimal(value string, maxValue int) (int, bool) {
	if value == "" || value[0] < '1' || value[0] > '9' {
		return 0, false
	}
	for i := 1; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(value)
	return n, err == nil && n <= maxValue
}

// ParseGHArgs accepts only the documented subcommands and produces typed tool
// input. The returned arguments may be rendered, but raw argv is never executed.
func ParseGHArgs(serverID string, rawArgv []string) (string, map[string]any, error) {
	if !registry.ValidID(serverID) || len(rawArgv) < 2 {
		return "", nil, denied()
	}
	group, action := rawArgv[0], rawArgv[1]
	if strings.Contains(group, ".") || strings.Contains(action, ".") {
		return "", nil, denied()
	}
	c, ok := findCapability(group + "." + action)
	if !ok {
		return "", nil, denied()
	}

	args := make(map[string]any, 4)
	for remaining := rawArgv[2:]; len(remaining) > 0; {
		arg := remaining[0]
		remaining = remaining[1:]
		if !strings.HasPrefix(arg, "-") {
			key := "number"
			if c.action == "repo.view" {
				key = "repo"
			} else if !c.number {
				return "", nil, denied()
			}
			if _, exists := args[key]; exists {
				return "", nil, denied()
			}
			args[key] = parseArgument(key, arg)
			continue
		}
		flag, value, inline := strings.Cut(arg, "=")
		key := flagArgument(flag, c)
		if _, exists := args[key]; exists || key == "" {
			return "", nil, denied()
		}
		if key == "method" {
			if inline {
				return "", nil, denied()
			}
			args[key] = strings.TrimPrefix(flag, "--")
			continue
		}
		if !inline {
			if len(remaining) == 0 || strings.HasPrefix(remaining[0], "-") {
				return "", nil, denied()
			}
			value = remaining[0]
			remaining = remaining[1:]
		}
		args[key] = parseArgument(key, value)
	}
	if !c.valid(args) {
		return "", nil, denied()
	}
	if c.states != nil {
		if _, ok := args["state"]; !ok {
			args["state"] = "open"
		}
		if _, ok := args["limit"]; !ok {
			args["limit"] = 30
		}
	}
	return serverID + "." + c.action, args, nil
}

func flagArgument(flag string, c capability) string {
	switch flag {
	case "-R", "--repo":
		return "repo"
	case "-s", "--state":
		if c.states != nil {
			return "state"
		}
	case "-L", "--limit":
		if c.states != nil {
			return "limit"
		}
	case "-b", "--body":
		if c.body {
			return "body"
		}
	case "--squash", "--merge", "--rebase":
		if c.merge {
			return "method"
		}
	}
	return ""
}

func parseArgument(key, value string) any {
	switch key {
	case "number", "limit":
		n, ok := decimal(value, maxNumber)
		if !ok {
			return nil
		}
		return n
	default:
		return value
	}
}

func argumentInt(value any, maxValue int) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, v >= 1 && v <= maxValue
	case json.Number:
		return decimal(string(v), maxValue)
	case float64:
		if v >= 1 && v <= float64(maxValue) && v == float64(int(v)) {
			return int(v), true
		}
	}
	return 0, false
}

func (c capability) valid(args map[string]any) bool {
	repo, ok := args["repo"].(string)
	if !ok || !validRepo(repo) {
		return false
	}
	if c.number {
		if _, ok := argumentInt(args["number"], maxNumber); !ok {
			return false
		}
	}
	if c.body {
		body, ok := args["body"].(string)
		if !ok || len(body) == 0 || len(body) > maxBody || strings.ContainsRune(body, 0) {
			return false
		}
	}
	if c.merge {
		method, ok := args["method"].(string)
		if !ok || !slices.Contains(mergeMethods, method) {
			return false
		}
	}
	for key, value := range args {
		switch key {
		case "repo":
		case "number":
			if !c.number {
				return false
			}
		case "body":
			if !c.body {
				return false
			}
		case "method":
			if !c.merge {
				return false
			}
		case "state":
			state, ok := value.(string)
			if !ok || !slices.Contains(c.states, state) {
				return false
			}
		case "limit":
			if _, ok := argumentInt(value, maxLimit); !ok || c.states == nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// RenderGHArgs validates typed input again, including input submitted directly
// through REST or MCP, and emits only the fixed command templates.
func RenderGHArgs(toolAction string, typedArgs map[string]any) ([]string, error) {
	c, ok := findCapability(toolAction)
	if !ok || !c.valid(typedArgs) {
		return nil, denied()
	}
	group, action, _ := strings.Cut(c.action, ".")
	repo := typedArgs["repo"].(string)
	argv := []string{group, action}
	if c.action != "repo.view" {
		argv = append(argv, "-R="+repo)
	}
	if c.fields != "" {
		argv = append(argv, "--json="+c.fields)
	}
	if c.states != nil {
		state, ok := typedArgs["state"].(string)
		if !ok {
			state = "open"
		}
		limit, ok := argumentInt(typedArgs["limit"], maxLimit)
		if !ok {
			limit = 30
		}
		argv = append(argv, "--state="+state, "--limit="+strconv.Itoa(limit))
	}
	if c.body {
		argv = append(argv, "--body="+typedArgs["body"].(string))
	}
	if c.merge {
		argv = append(argv, "--"+typedArgs["method"].(string)+"=true")
	}
	if c.action == "repo.view" {
		argv = append(argv, "--", repo)
	}
	if c.number {
		number, _ := argumentInt(typedArgs["number"], maxNumber)
		argv = append(argv, "--", strconv.Itoa(number))
	}
	return argv, nil
}

func Tools(serverID string) []registry.Tool {
	tools := make([]registry.Tool, 0, len(capabilities))
	for _, c := range capabilities {
		props := map[string]any{"repo": map[string]any{"type": "string", "description": "GitHub repository in OWNER/REPO form"}}
		required := []string{"repo"}
		if c.number {
			props["number"] = integerSchema(maxNumber)
			required = append(required, "number")
		}
		if c.body {
			props["body"] = map[string]any{"type": "string", "minLength": 1, "maxLength": maxBody, "description": "Comment body, up to 65536 UTF-8 bytes"}
			required = append(required, "body")
		}
		if c.merge {
			props["method"] = map[string]any{"type": "string", "enum": slices.Clone(mergeMethods)}
			required = append(required, "method")
		}
		if c.states != nil {
			props["state"] = map[string]any{"type": "string", "enum": slices.Clone(c.states), "default": "open"}
			props["limit"] = integerSchema(maxLimit)
		}
		readOnly := c.fields != ""
		actors := []string{"developer"}
		if readOnly {
			actors = append(actors, "observer")
		}
		tools = append(tools, registry.Tool{
			ID: serverID + "." + c.action, ServerID: serverID, Name: c.action,
			Description: c.description + " on GitHub", ReadOnly: readOnly, Actors: actors,
			InputSchema: map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false},
		})
	}
	return tools
}

func integerSchema(maxValue int) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": maxValue}
}
