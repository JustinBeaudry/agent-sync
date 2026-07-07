package harness

import "github.com/agent-sync/agent-sync/internal/ir"

type Layer struct {
	Scope     string
	Nodes     []ir.Node
	Skills    map[string]ir.Skill
	Fragments []Fragment
	SourceURL string
	Commit    string
}

func ResolveNodes(layers []Layer, targetScope string) ([]ir.Node, map[string]ir.Skill) {
	order := make([]string, 0, len(layers))
	byID := map[string]ir.Node{}
	skills := map[string]ir.Skill{}

	for i, layer := range layers {
		isCurrent := i == len(layers)-1
		if !isCurrent && !portableLayerInherits(layer.Scope, targetScope) {
			continue
		}

		for _, node := range layer.Nodes {
			id := string(node.Kind) + "\x00" + node.ID
			if _, seen := byID[id]; !seen {
				order = append(order, id)
			}
			if !isCurrent {
				node.SourceURL = layer.SourceURL
				node.SourceCommit = layer.Commit
			}
			byID[id] = node
			if node.Kind == ir.KindSkill {
				delete(skills, node.ID)
				if sk, ok := layer.Skills[node.ID]; ok {
					skills[node.ID] = sk
				}
			}
		}
	}

	outs := make([]ir.Node, 0, len(order))
	for _, id := range order {
		outs = append(outs, byID[id])
	}
	return outs, skills
}

func portableLayerInherits(scope, targetScope string) bool {
	if scope == targetScope {
		return true
	}
	switch scope {
	case "workspace", "project", "directory", "global":
		return true
	default:
		return false
	}
}

func ResolveFragments(layers []Layer, targetScope string) []Fragment {
	order := make([]string, 0, len(layers))
	byIdentity := map[string]Fragment{}

	for i, layer := range layers {
		isCurrent := i == len(layers)-1
		for _, fragment := range layer.Fragments {
			if !isCurrent && !canInherit(fragment, targetScope) {
				continue
			}
			id := fragment.Identity()
			if _, seen := byIdentity[id]; !seen {
				order = append(order, id)
			}
			byIdentity[id] = fragment
		}
	}

	out := make([]Fragment, 0, len(order))
	for _, id := range order {
		out = append(out, byIdentity[id])
	}
	return out
}

func canInherit(f Fragment, targetScope string) bool {
	if f.Scope == targetScope {
		return true
	}
	if f.Inheritance != InheritanceDescendants {
		return false
	}
	if f.Visibility != VisibilityTeam {
		return false
	}
	return true
}
