package swarm

// Canonical domain names. The swarm package remains as a source-compatible
// implementation boundary while callers migrate to subagent terminology.
type SubagentManager = Swarm
type SubagentConfig = Config
type Subagent = Agent
type SubagentSnapshot = AgentSnapshot
type SubagentRequest = SpawnRequest

func NewSubagentManager(cfg SubagentConfig) *SubagentManager { return New(cfg) }
