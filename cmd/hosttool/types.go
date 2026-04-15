package hosttool

// Tool describes a command the agent can trigger on the host.
type Tool struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Cmd         string `yaml:"cmd" json:"cmd,omitempty"`
	Args        []Arg  `yaml:"args" json:"args,omitempty"`
}

// Arg describes a single parameter an agent may pass to a host tool.
type Arg struct {
	Name        string         `yaml:"name" json:"name"`
	Description string         `yaml:"description" json:"description,omitempty"`
	Type        string         `yaml:"type" json:"type,omitempty"`         // string|integer|number|boolean
	Required    *bool          `yaml:"required" json:"required,omitempty"` // default true
	Default     any            `yaml:"default" json:"default,omitempty"`
	Enum        []string       `yaml:"enum" json:"enum,omitempty"`
	Regex       string         `yaml:"regex" json:"regex,omitempty"`
	Min         *float64       `yaml:"min" json:"min,omitempty"`
	Max         *float64       `yaml:"max" json:"max,omitempty"`
	MinLength   *int           `yaml:"min_length" json:"min_length,omitempty"`
	MaxLength   *int           `yaml:"max_length" json:"max_length,omitempty"`
	URL         *URLConstraint `yaml:"url" json:"url,omitempty"`
	Validate    string         `yaml:"validate" json:"validate,omitempty"` // host-side only; sync.go strips before writing sandbox JSON
}

// URLConstraint restricts acceptable URLs for a string arg.
type URLConstraint struct {
	Schemes         []string `yaml:"schemes" json:"schemes,omitempty"`
	Hosts           []string `yaml:"hosts" json:"hosts,omitempty"`
	PathPrefix      string   `yaml:"path_prefix" json:"path_prefix,omitempty"`
	BlockPrivateIPs *bool    `yaml:"block_private_ips" json:"block_private_ips,omitempty"` // default true
}
