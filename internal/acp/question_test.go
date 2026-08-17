package acp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/crush/internal/question"
	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// askQuestion drives a question batch through the workspace question
// service (the same path the question tool uses) and returns what Ask
// yields. Ask blocks until the watcher relays the client's answer, so
// the test context bounds the wait and fails the test on a hang.
func askQuestion(t *testing.T, sess *session, req question.Request) ([]question.Answer, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		answers []question.Answer
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		answers, err := sess.workspace.App.Questions.Ask(ctx, req)
		ch <- result{answers, err}
	}()
	select {
	case r := <-ch:
		return r.answers, r.err
	case <-ctx.Done():
		t.Fatal("question Ask hung (no answer relayed)")
		return nil, ctx.Err()
	}
}

// TestQuestionForwardsYesNo verifies US-022 for a single yes_no
// question: the batch surfaces as one requestPermission with Yes/No
// options and the client's choice flows back as a Yes answer.
func TestQuestionForwardsYesNo(t *testing.T) {
	_, sess, sid, cap, _ := permissionEnv(t)
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("yes")}, nil
	}

	answers, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_1",
		Questions: []question.Question{{
			Type:        question.TypeYesNo,
			Text:        "Continue?",
			Description: "Proceed with the operation?",
		}},
	})
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.NotNil(t, answers[0].Yes)
	require.True(t, *answers[0].Yes)

	cap.mu.Lock()
	require.Len(t, cap.permissionRequests, 1)
	pr := cap.permissionRequests[0]
	cap.mu.Unlock()
	require.Equal(t, sid, pr.SessionId)
	require.Equal(t, acpsdk.ToolCallId("call_1"), pr.ToolCall.ToolCallId)
	require.NotNil(t, pr.ToolCall.Title)
	require.Equal(t, "Continue?", *pr.ToolCall.Title)
	require.Len(t, pr.Options, 2)
	require.Equal(t, acpsdk.PermissionOptionId("yes"), pr.Options[0].OptionId)
	require.Equal(t, "Yes", pr.Options[0].Name)
	require.Equal(t, acpsdk.PermissionOptionId("no"), pr.Options[1].OptionId)
	require.Equal(t, "No", pr.Options[1].Name)
}

// TestQuestionForwardsSingleChoice verifies US-022 for a single_choice
// question: choices become permission options and the chosen option ID
// flows back as the selected answer.
func TestQuestionForwardsSingleChoice(t *testing.T) {
	_, sess, sid, cap, _ := permissionEnv(t)
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("b")}, nil
	}

	answers, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_2",
		Questions: []question.Question{{
			Type:        question.TypeSingleChoice,
			Text:        "Which approach?",
			Description: "Pick one",
			Choices: []question.Choice{
				{ID: "a", Label: "Plan A"},
				{ID: "b", Label: "Plan B"},
			},
		}},
	})
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Equal(t, []string{"b"}, answers[0].SelectedIDs)

	cap.mu.Lock()
	require.Len(t, cap.permissionRequests, 1)
	pr := cap.permissionRequests[0]
	cap.mu.Unlock()
	require.Equal(t, sid, pr.SessionId)
	require.Equal(t, "Which approach?", *pr.ToolCall.Title)
	require.Len(t, pr.Options, 2)
	require.Equal(t, acpsdk.PermissionOptionId("a"), pr.Options[0].OptionId)
	require.Equal(t, "Plan A", pr.Options[0].Name)
	require.Equal(t, acpsdk.PermissionOptionId("b"), pr.Options[1].OptionId)
	require.Equal(t, "Plan B", pr.Options[1].Name)
}

// TestQuestionBatchSerial verifies a multi-question batch is relayed one
// requestPermission per question and answered in one Answer call.
func TestQuestionBatchSerial(t *testing.T) {
	_, sess, _, cap, _ := permissionEnv(t)
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("yes")}, nil
	}

	answers, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_3",
		Questions: []question.Question{
			{Type: question.TypeYesNo, Text: "Q1?", Description: "d1"},
			{Type: question.TypeYesNo, Text: "Q2?", Description: "d2"},
		},
	})
	require.NoError(t, err)
	require.Len(t, answers, 2)
	require.True(t, *answers[0].Yes)
	require.True(t, *answers[1].Yes)

	cap.mu.Lock()
	require.Len(t, cap.permissionRequests, 2)
	for _, pr := range cap.permissionRequests {
		require.NotNil(t, pr.ToolCall.Title)
		require.Contains(t, *pr.ToolCall.Title, "Ready to go?")
	}
	cap.mu.Unlock()
}

// TestQuestionCancelledByClient verifies the client cancel path: the
// pending question is cancelled and Ask returns ErrCancelled.
func TestQuestionCancelledByClient(t *testing.T) {
	_, sess, _, cap, _ := permissionEnv(t)
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}

	_, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_4",
		Questions:  []question.Question{{Type: question.TypeYesNo, Text: "Go?", Description: "d"}},
	})
	require.ErrorIs(t, err, question.ErrCancelled)
}

// TestQuestionClientErrorCancels verifies a client failure cancels the
// pending question so Ask never hangs (safety first, like permissions).
func TestQuestionClientErrorCancels(t *testing.T) {
	_, sess, _, cap, _ := permissionEnv(t)
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{}, errors.New("client exploded")
	}

	_, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_5",
		Questions:  []question.Question{{Type: question.TypeYesNo, Text: "Go?", Description: "d"}},
	})
	require.ErrorIs(t, err, question.ErrCancelled)
}

// TestQuestionTimeoutCancels verifies a silent client times out and the
// question is cancelled instead of hanging.
func TestQuestionTimeoutCancels(t *testing.T) {
	_, sess, _, cap, _ := permissionEnv(t)
	old := questionRequestTimeout
	questionRequestTimeout = 150 * time.Millisecond
	defer func() { questionRequestTimeout = old }()
	cap.permissionReply = func(acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		time.Sleep(600 * time.Millisecond)
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("yes")}, nil
	}

	_, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_6",
		Questions:  []question.Question{{Type: question.TypeYesNo, Text: "Go?", Description: "d"}},
	})
	require.ErrorIs(t, err, question.ErrCancelled)
}

// TestQuestionNoConnectionAutoAnswers verifies the in-process fallback:
// with no client attached the batch is auto-answered (yes_no -> yes,
// single_choice -> first choice) so Ask returns immediately.
func TestQuestionNoConnectionAutoAnswers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	a := newEnvAgent(t, "")
	sid := newSessionOn(t, a, t.TempDir())
	sess := sessionFor(t, a, sid)
	t.Cleanup(func() { a.backend.DetachClient(sess.workspace.ID, a.clientID) })

	answers, err := askQuestion(t, sess, question.Request{
		SessionID:  sess.sessionID,
		ToolCallID: "call_7",
		Questions: []question.Question{
			{Type: question.TypeYesNo, Text: "Go?", Description: "d1"},
			{
				Type:        question.TypeSingleChoice,
				Text:        "Pick?",
				Description: "d2",
				Choices:     []question.Choice{{ID: "x", Label: "X"}, {ID: "y", Label: "Y"}},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, answers, 2)
	require.True(t, *answers[0].Yes)
	require.Equal(t, []string{"x"}, answers[1].SelectedIDs)
}

// TestQuestionUnsupportedTypeCancels verifies US-023's degradation: a
// multi_choice or free_text question cannot be carried by permission
// options, so the batch is cancelled and Ask returns promptly instead
// of hanging.
func TestQuestionUnsupportedTypeCancels(t *testing.T) {
	for _, qType := range []question.Type{question.TypeMultiChoice, question.TypeFreeText} {
		t.Run(string(qType), func(t *testing.T) {
			_, sess, _, _, _ := permissionEnv(t)

			var q question.Question
			switch qType {
			case question.TypeMultiChoice:
				q = question.Question{
					Type:        qType,
					Text:        "Pick many?",
					Description: "d",
					Choices:     []question.Choice{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
				}
			case question.TypeFreeText:
				q = question.Question{Type: qType, Text: "Type?", Description: "d"}
			}

			_, err := askQuestion(t, sess, question.Request{
				SessionID:  sess.sessionID,
				ToolCallID: "call_8",
				Questions:  []question.Question{q},
			})
			require.ErrorIs(t, err, question.ErrCancelled)
		})
	}
}
