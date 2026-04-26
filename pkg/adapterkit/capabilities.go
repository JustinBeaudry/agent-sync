package adapterkit

import "encoding/json"

// CapabilitiesBuilder builds Capabilities values fluently. When the same
// kind is set multiple times, the last write wins.
type CapabilitiesBuilder struct {
	caps Capabilities
}

func NewCapabilities() *CapabilitiesBuilder {
	return &CapabilitiesBuilder{
		caps: Capabilities{
			ConceptKinds: map[string]CapabilityLevel{},
		},
	}
}

func (b *CapabilitiesBuilder) Supports(kind string) *CapabilitiesBuilder {
	return b.set(kind, CapabilitySupported)
}

func (b *CapabilitiesBuilder) Partial(kind string) *CapabilitiesBuilder {
	return b.set(kind, CapabilityPartial)
}

func (b *CapabilitiesBuilder) Unsupported(kind string) *CapabilitiesBuilder {
	return b.set(kind, CapabilityUnsupported)
}

func (b *CapabilitiesBuilder) WithWriteToolOwned(enabled bool) *CapabilitiesBuilder {
	b.caps.WriteToolOwned = enabled
	return b
}

func (b *CapabilitiesBuilder) WithProgress(enabled bool) *CapabilitiesBuilder {
	b.caps.Progress = enabled
	return b
}

func (b *CapabilitiesBuilder) Build() Capabilities {
	out := Capabilities{
		ConceptKinds:   make(map[string]CapabilityLevel, len(b.caps.ConceptKinds)),
		WriteToolOwned: b.caps.WriteToolOwned,
		Progress:       b.caps.Progress,
		Meta:           cloneRawMessage(b.caps.Meta),
	}
	for kind, level := range b.caps.ConceptKinds {
		out.ConceptKinds[kind] = level
	}
	return out
}

func (b *CapabilitiesBuilder) set(kind string, level CapabilityLevel) *CapabilitiesBuilder {
	if b.caps.ConceptKinds == nil {
		b.caps.ConceptKinds = map[string]CapabilityLevel{}
	}
	b.caps.ConceptKinds[kind] = level
	return b
}

func cloneRawMessage(src json.RawMessage) json.RawMessage {
	if src == nil {
		return nil
	}
	return append(json.RawMessage(nil), src...)
}
