package runner

import (
	"encoding/json"
)

func (d *acpDialogue) request(step acpStep) error {
	d.nextID++
	d.pending[d.nextID] = step
	return d.write(map[string]any{"jsonrpc": "2.0", "id": d.nextID, "method": step.method, "params": step.params})
}

func (d *acpDialogue) respond(id json.RawMessage, result any) {
	_ = d.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (d *acpDialogue) respondError(id json.RawMessage, code int, msg string) {
	_ = d.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (d *acpDialogue) write(v any) error {
	if d.w == nil || d.finished {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = d.w.Write(append(b, '\n'))
	return err
}
