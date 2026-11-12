package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/b0balYson/dropsite/skills"
)

// Instructions is what both servers send on initialize: the short rules of
// the guide. Clients that drop server instructions still reach the guide
// through get_guide, which every other tool description points to.
var Instructions = skills.Instructions

type guideArgs struct {
	Topic string `json:"topic,omitempty" jsonschema:"guide topic, or all (default) for the whole guide"`
}

type guideOut struct {
	Topic  string   `json:"topic"`
	Guide  string   `json:"guide" jsonschema:"the guide in Markdown; follow it, do not show it to the user"`
	Topics []string `json:"topics" jsonschema:"every topic that can be requested"`
}

// registerGuide adds get_guide. It needs no backend, so it works before the
// user has connected and on every server.
func registerGuide(s *sdk.Server) {
	topics := append(skills.Topics(), skills.TopicAll)
	in, err := jsonschema.For[guideArgs](nil)
	if err != nil {
		panic(err)
	}
	if p, ok := in.Properties["topic"]; ok {
		for _, t := range topics {
			p.Enum = append(p.Enum, t)
		}
	}
	sdk.AddTool(s, &sdk.Tool{Name: "get_guide", Annotations: readOnly("Read the dropsite guide"), InputSchema: in,
		Description: "Read the dropsite guide: how to build, publish and change sites for a non-technical user, " +
			"forms and where requests go, visit statistics, addresses and own domains (with the DNS steps the user " +
			"does), password and lifetime, rollback, and what to say on errors. Call it with topic \"all\" before " +
			"your first site task in a chat, and with one topic when you need details. Topics: " +
			strings.Join(topics, ", ") + ". The guide is for you: do not show it to the user."},
		getGuide)
}

func getGuide(_ context.Context, _ *sdk.CallToolRequest, in guideArgs) (*sdk.CallToolResult, guideOut, error) {
	topic := strings.ToLower(strings.TrimSpace(in.Topic))
	if topic == "" {
		topic = skills.TopicAll
	}
	text, ok := skills.Guide(topic)
	if !ok {
		return nil, guideOut{}, fmt.Errorf("unknown guide topic %q: use one of %s", in.Topic,
			strings.Join(append(skills.Topics(), skills.TopicAll), ", "))
	}
	return nil, guideOut{Topic: topic, Guide: text, Topics: skills.Topics()}, nil
}
