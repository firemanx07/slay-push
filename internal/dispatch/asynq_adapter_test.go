package dispatch

import (
	"context"
	"testing"

	"github.com/hibiken/asynq"

	"github.com/firemanx07/slay-push/internal/crypto"
	"github.com/firemanx07/slay-push/internal/targeting"
)

func TestAsynqFanoutHandler_MalformedPayload(t *testing.T) {
	h := &Handlers{Targeting: targeting.NewRegistry(nil), Crypto: crypto.MasterKey{}}
	task := asynq.NewTask("fanout", []byte(`{not json`))
	if err := h.AsynqFanoutHandler(context.Background(), task); err == nil {
		t.Fatal("expected an error for a malformed fanout payload")
	}
}

func TestAsynqSendHandler_MalformedPayload(t *testing.T) {
	h := &Handlers{Targeting: targeting.NewRegistry(nil), Crypto: crypto.MasterKey{}}
	task := asynq.NewTask("send:expo", []byte(`{not json`))
	if err := h.AsynqSendHandler(context.Background(), task); err == nil {
		t.Fatal("expected an error for a malformed send payload")
	}
}
