package domain

// AgentType is the runtime representation of a configured agent type.
type AgentType struct {
	Name           string
	Description    string
	InstanceName   string
	Harness        string
	Workspace      string
	PromptPath     string
	ApprovalPolicy string
	MaxConcurrent  int
	Env            map[string]string
	Tools          []string
	Skills         []string
	Context        string
}
