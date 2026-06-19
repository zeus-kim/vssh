package main

import (
	"fmt"

	"github.com/zeus-kim/vssh/internal/workflow"
)

func toolWorkflowList(_ map[string]interface{}) map[string]interface{} {
	wfs := workflow.List()
	out := make([]map[string]interface{}, 0, len(wfs))
	for _, w := range wfs {
		out = append(out, map[string]interface{}{
			"name":        w.Name,
			"description": w.Description,
			"params":      w.Params,
			"steps":       len(w.Steps),
		})
	}
	return map[string]interface{}{
		"success":   true,
		"tool":      "vssh_workflow_list",
		"count":     len(out),
		"workflows": out,
	}
}

func toolWorkflowRun(args map[string]interface{}) map[string]interface{} {
	name := getString(args, "name")
	if name == "" {
		return wfErr("vssh_workflow_run", "missing_argument", "name is required")
	}
	w, ok := workflow.Get(name)
	if !ok {
		return wfErr("vssh_workflow_run", "not_found", "no workflow "+name)
	}
	params := map[string]string{}
	for k, v := range getParamsMap(args, "params") {
		params[k] = fmt.Sprintf("%v", v)
	}
	if err := w.Validate(params); err != nil {
		return wfErr("vssh_workflow_run", "invalid_params", err.Error())
	}
	dryRun := getBool(args, "dry_run", false)
	target := getString(args, "target")
	if target == "" && !dryRun {
		return wfErr("vssh_workflow_run", "missing_argument", "target is required unless dry_run=true")
	}

	runID := newRunID(name)
	var execFn workflow.ExecFunc
	if !dryRun {
		execFn = nativeExecFunc(target)
	}
	res := w.Run(runID, target, params, dryRun, execFn)
	if err := workflow.SaveRun(res); err != nil {
		res.Summary += " (warning: run not persisted: " + err.Error() + ")"
	}
	return map[string]interface{}{
		"success": res.Status != "aborted" && res.Status != "failed",
		"tool":    "vssh_workflow_run",
		"run":     res,
	}
}

func toolWorkflowStatus(args map[string]interface{}) map[string]interface{} {
	runID := getString(args, "run_id")
	if runID == "" {
		return wfErr("vssh_workflow_status", "missing_argument", "run_id is required")
	}
	res, err := workflow.LoadRun(runID)
	if err != nil {
		return wfErr("vssh_workflow_status", "not_found", err.Error())
	}
	return map[string]interface{}{
		"success": true,
		"tool":    "vssh_workflow_status",
		"run":     res,
	}
}

func wfErr(tool, code, msg string) map[string]interface{} {
	return map[string]interface{}{
		"success": false,
		"tool":    tool,
		"error":   map[string]interface{}{"code": code, "message": msg},
	}
}
