package taskmanager

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/jspc/workflow-engine/datastore"
	"github.com/jspc/workflow-engine/workflow"

	"github.com/fatih/structs"
)

// MasterManager ...
// Container for Master Task manager configuration
type MasterManager struct {
	datastore datastore.Datastore
}

// NewMasterManager ...
// Initialise and return a Master Task Manager
func NewMasterManager(redisURI string) (m MasterManager) {
	var err error

	if m.datastore, err = datastore.NewDatastore(redisURI); err != nil {
		log.Fatal(err)
	}

	return
}

// Consume ...
// Handle json from the message queue; for a Master node these will be responses.
// Parse messages, update Workflow contexts, write to database and call next step
func (m MasterManager) Consume(body string) (output map[string]interface{}, err error) {
	var b interface{}
	var uuid string
	var wfr workflow.WorkflowRunner

	if err = json.Unmarshal([]byte(body), &b); err != nil {
		return
	}

	output = b.(map[string]interface{})
	uuid := output["UUID"].(string)
	if wfr, err = m.datastore.LoadWorkflowRunner(uuid); err != nil {
		return
	}

	idx, step := wfr.Current()
	step.SetStatus(output)
	wfr.Workflow.Steps[idx] = step

	switch output["Register"].(type) {
	case string:
		register := output["Register"].(string)

		switch output["Data"].(type) {
		case map[string]interface{}:
			data := output["Data"].(map[string]interface{})
			wfr.Variables[register] = data

		default:
			log.Println("Not registering output: got garbage back")
		}
	}

	if output["Failed"].(bool) {
		wfr.Fail(fmt.Sprintf("Step %q failed. See below", wfr.Last))
		m.datastore.DumpWorkflowRunner(wfr)
		return
	}

	m.datastore.DumpWorkflowRunner(wfr)

	s, done := m.Continue(wfr.UUID)
	if !done {
		output = structs.Map(s)
	}

	return
}

// Load ...
// Load a workflow from storage and create a WorkflowRunner state machine
func (m MasterManager) Load(name string, variables map[string]interface{}) (uuid string, err error) {
	wf, err := m.datastore.LoadWorkflow(name)
	if err != nil {
		return
	}

	wfr := workflow.NewWorkflowRunner(u, wf)

	switch variables.(type) {
	case map[string]interface{}:
		wfr.Variables["Runtime"] = variables
	}

	wfr.Start()

	m.datastore.DumpWorkflowRunner(wfr)

	return wfr.UUID, nil
}

// Continue ...
// Should there be a next step in the workflow, compile step templates
// and push the step to the emssage queue
func (m MasterManager) Continue(uuid string) {
	wfr, err := m.datastore.LoadWorkflowRunner(uuid)
	if err != nil {
		log.Print(err)
		return
	}

	step, done := wfr.Next()

	if done {
		wfr.End()
	} else {
		err := step.Compile(wfr.Variables)
		if err != nil {
			wfr.Fail(fmtError(step, err))
			m.datastore.DumpWorkflowRunner(wfr)

			return
		}

		step.UUID = wfr.UUID

		j, err := step.JSON()
		if err != nil {
			wfr.Fail(fmtError(step, err))
			m.datastore.DumpWorkflowRunner(wfr)

			return
		}

		if err := node.Producer.send(j); err != nil {
			wfr.Fail(fmtError(step, err))
		}

		wfr.Last = step.Name
	}

	m.datastore.DumpWorkflowRunner(wfr)
}

func fmtError(step workflow.Step, err error) string {
	return fmt.Sprintf("%s: %s", step.Name, err.Error())
}