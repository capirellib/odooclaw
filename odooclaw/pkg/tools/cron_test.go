package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nicolasramos/odooclaw/pkg/bus"
	"github.com/nicolasramos/odooclaw/pkg/cron"
)

func newTestCronTool(t *testing.T) (*CronTool, *bus.MessageBus) {
	t.Helper()
	execTool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}
	msgBus := bus.NewMessageBus()
	return &CronTool{execTool: execTool, msgBus: msgBus}, msgBus
}

func drainOutbound(t *testing.T, msgBus *bus.MessageBus) (bus.OutboundMessage, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return msgBus.SubscribeOutbound(ctx)
}

// A recurring watchdog-style command that succeeds with no output must not
// flood the chat on every run — this was the actual bug: every 5 minutes a
// silent health-check script posted "Scheduled command '...' executed:
// (no output)" forever.
func TestRecurringSilentCommandDoesNotPublish(t *testing.T) {
	ct, msgBus := newTestCronTool(t)
	everyMS := int64(300000)
	job := &cron.CronJob{
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Payload: cron.CronPayload{
			Command: "true", // exits 0, prints nothing
			Channel: "odoo",
			To:      "42",
		},
	}

	if got := ct.ExecuteJob(context.Background(), job); got != "ok" {
		t.Fatalf("ExecuteJob returned %q, want ok", got)
	}
	if _, ok := drainOutbound(t, msgBus); ok {
		t.Fatal("expected no chat message for a silent recurring command")
	}
}

// A recurring command that actually has something to say still gets posted.
func TestRecurringCommandWithOutputStillPublishes(t *testing.T) {
	ct, msgBus := newTestCronTool(t)
	everyMS := int64(300000)
	job := &cron.CronJob{
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Payload: cron.CronPayload{
			Command: "echo mcp-down",
			Channel: "odoo",
			To:      "42",
		},
	}

	ct.ExecuteJob(context.Background(), job)
	msg, ok := drainOutbound(t, msgBus)
	if !ok {
		t.Fatal("expected a chat message when the command produced output")
	}
	if want := "mcp-down"; !strings.Contains(msg.Content, want) {
		t.Fatalf("message %q does not contain %q", msg.Content, want)
	}
}

// A failing recurring command must always be reported, silent or not.
func TestRecurringFailingCommandStillPublishes(t *testing.T) {
	ct, msgBus := newTestCronTool(t)
	everyMS := int64(300000)
	job := &cron.CronJob{
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Payload: cron.CronPayload{
			Command: "false",
			Channel: "odoo",
			To:      "42",
		},
	}

	ct.ExecuteJob(context.Background(), job)
	if _, ok := drainOutbound(t, msgBus); !ok {
		t.Fatal("expected a chat message when the command failed")
	}
}

// A one-time job always confirms it ran, even with no output: there is no
// next run to fall back on, so silence here would look like nothing happened.
func TestOneTimeSilentCommandStillPublishes(t *testing.T) {
	ct, msgBus := newTestCronTool(t)
	atMS := time.Now().UnixMilli()
	job := &cron.CronJob{
		Schedule: cron.CronSchedule{Kind: "at", AtMS: &atMS},
		Payload: cron.CronPayload{
			Command: "true",
			Channel: "odoo",
			To:      "42",
		},
	}

	ct.ExecuteJob(context.Background(), job)
	if _, ok := drainOutbound(t, msgBus); !ok {
		t.Fatal("expected a confirmation message for a one-time command")
	}
}
