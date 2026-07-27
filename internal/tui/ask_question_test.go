package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kaiizer777/triad/internal/agent"
	"github.com/kaiizer777/triad/internal/loop"
)

func TestAskQuestion_SingleQuestion(t *testing.T) {
	m := Model{
		sessionState: loop.StateAskQuestion,
		styles:       DefaultStyles(),
	}
	tc := agent.ToolCall{ID: "tc-123"}
	
	batch := agent.AskQuestionBatch{
		Questions: []agent.AskQuestion{
			{
				Question: "Which color?",
				Options: []agent.AskQuestionOption{
					{Label: "Red"},
					{Label: "Blue"},
				},
				AllowMultiSelect: false,
			},
		},
	}
	m.askQuestion = &askQuestionState{
		Batch:        batch,
		SelectedOpts: make(map[int]map[int]bool),
		OriginalCall: tc,
	}

	// Move down to Blue
	m.handleAskQuestionKey(makeKeyMsg("down"))
	
	// Submit
	cmd, _ := m.handleAskQuestionKey(makeKeyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected command on submit")
	}

	msg := cmd()
	resMsg, ok := msg.(toolResultMsg)
	if !ok {
		t.Fatalf("expected toolResultMsg, got %T", msg)
	}
	if resMsg.toolCall.ID != "tc-123" {
		t.Errorf("expected tc-123, got %s", resMsg.toolCall.ID)
	}

	var answers []string
	if err := json.Unmarshal([]byte(resMsg.result), &answers); err != nil {
		t.Fatalf("invalid json result: %v", err)
	}
	if len(answers) != 1 || answers[0] != "Blue" {
		t.Errorf("expected [Blue], got %v", answers)
	}
}

func TestAskQuestion_OtherFreeText(t *testing.T) {
	m := Model{
		sessionState: loop.StateAskQuestion,
		styles:       DefaultStyles(),
	}
	tc := agent.ToolCall{ID: "tc-124"}
	
	batch := agent.AskQuestionBatch{
		Questions: []agent.AskQuestion{
			{
				Question: "Which color?",
				Options: []agent.AskQuestionOption{
					{Label: "Red"},
				},
				AllowMultiSelect: false,
			},
		},
	}
	m.askQuestion = &askQuestionState{
		Batch:        batch,
		SelectedOpts: make(map[int]map[int]bool),
		OriginalCall: tc,
	}

	// Move down to "Other"
	m.handleAskQuestionKey(makeKeyMsg("down"))
	
	// Enter to activate Other
	m.handleAskQuestionKey(makeKeyMsg("enter"))
	if !m.askQuestion.OtherActive {
		t.Fatal("expected OtherActive to be true")
	}

	// Type "Green"
	for _, r := range "Green" {
		m.handleAskQuestionKey(makeKeyMsg(string(r)))
	}

	// Submit Other
	cmd, _ := m.handleAskQuestionKey(makeKeyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected command on submit")
	}

	msg := cmd()
	resMsg := msg.(toolResultMsg)

	var answers []string
	if err := json.Unmarshal([]byte(resMsg.result), &answers); err != nil {
		t.Fatalf("invalid json result: %v", err)
	}
	if len(answers) != 1 || answers[0] != "Green" {
		t.Errorf("expected [Green], got %v", answers)
	}
}

func TestAskQuestion_MultiSelect(t *testing.T) {
	m := Model{
		sessionState: loop.StateAskQuestion,
		styles:       DefaultStyles(),
	}
	tc := agent.ToolCall{ID: "tc-125"}
	
	batch := agent.AskQuestionBatch{
		Questions: []agent.AskQuestion{
			{
				Question: "Which toppings?",
				Options: []agent.AskQuestionOption{
					{Label: "Cheese"},
					{Label: "Pepperoni"},
					{Label: "Onions"},
				},
				AllowMultiSelect: true,
			},
		},
	}
	m.askQuestion = &askQuestionState{
		Batch:        batch,
		SelectedOpts: make(map[int]map[int]bool),
		OriginalCall: tc,
	}

	// Space on Cheese
	m.handleAskQuestionKey(makeKeyMsg(" "))
	
	// Down to Pepperoni
	m.handleAskQuestionKey(makeKeyMsg("down"))
	
	// Down to Onions
	m.handleAskQuestionKey(makeKeyMsg("down"))
	
	// Space on Onions
	m.handleAskQuestionKey(makeKeyMsg(" "))

	// Submit
	cmd, _ := m.handleAskQuestionKey(makeKeyMsg("enter"))
	msg := cmd()
	resMsg := msg.(toolResultMsg)

	var answers []string
	json.Unmarshal([]byte(resMsg.result), &answers)
	if !strings.Contains(answers[0], "Cheese") || !strings.Contains(answers[0], "Onions") || strings.Contains(answers[0], "Pepperoni") {
		t.Errorf("expected Cheese and Onions, got %v", answers)
	}
}

func TestAskQuestion_MultiQuestionBatchBlocking(t *testing.T) {
	m := Model{
		sessionState: loop.StateAskQuestion,
		styles:       DefaultStyles(),
	}
	tc := agent.ToolCall{ID: "tc-126"}
	
	batch := agent.AskQuestionBatch{
		Questions: []agent.AskQuestion{
			{
				Question: "Q1?",
				Options: []agent.AskQuestionOption{{Label: "A"}, {Label: "B"}},
			},
			{
				Question: "Q2?",
				Options: []agent.AskQuestionOption{{Label: "C"}, {Label: "D"}},
			},
		},
	}
	m.askQuestion = &askQuestionState{
		Batch:        batch,
		SelectedOpts: make(map[int]map[int]bool),
		OriginalCall: tc,
	}

	// Q1: submit A
	cmd, _ := m.handleAskQuestionKey(makeKeyMsg("enter"))
	if cmd != nil {
		t.Fatal("expected nil command after Q1, blocking for Q2")
	}
	if m.askQuestion.CurrentIndex != 1 {
		t.Errorf("expected CurrentIndex=1, got %d", m.askQuestion.CurrentIndex)
	}

	// Q2: submit C
	cmd2, _ := m.handleAskQuestionKey(makeKeyMsg("enter"))
	if cmd2 == nil {
		t.Fatal("expected cmd after Q2")
	}

	res := cmd2().(toolResultMsg)
	var answers []string
	json.Unmarshal([]byte(res.result), &answers)
	if len(answers) != 2 || answers[0] != "A" || answers[1] != "C" {
		t.Errorf("expected [A C], got %v", answers)
	}
}
