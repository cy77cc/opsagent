package main

import (
	"context"
	"log"

	"github.com/cy77cc/opsagent/sdk/plugin"
)

type EchoHandler struct{}

func (h *EchoHandler) Init(cfg map[string]interface{}) error {
	log.Println("echo plugin initialized")
	return nil
}

func (h *EchoHandler) TaskTypes() []string {
	return []string{"echo"}
}

func (h *EchoHandler) Execute(_ context.Context, req *plugin.TaskRequest) (*plugin.TaskResponse, error) {
	log.Printf("executing task %s with params: %v", req.TaskID, req.Params)
	return &plugin.TaskResponse{
		TaskID: req.TaskID,
		Status: "ok",
		Data: map[string]interface{}{
			"echo": req.Params,
			"task": req.TaskType,
		},
	}, nil
}

func (h *EchoHandler) Shutdown(_ context.Context) error {
	log.Println("echo plugin shutting down")
	return nil
}

func (h *EchoHandler) HealthCheck(_ context.Context) error {
	return nil
}

func main() {
	if err := plugin.Serve(&EchoHandler{}); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
