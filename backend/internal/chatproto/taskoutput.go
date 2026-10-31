package chatproto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
)

// TaskOutput is where a background task's output file is, as claude reported
// it in the log, and the claude session ids the log's system/init lines name.
type TaskOutput struct {
	// Path is claude's own path for the file, unchecked. Empty when the log
	// does not name one yet.
	Path             string
	ClaudeSessionIDs []string
}

var outputPathInResult = regexp.MustCompile(`Output is being written to: (\S+\.output)`)

// FindTaskOutput scans an event log for taskID's output file. It reads only
// lines claude wrote: the task's task_notification output_file wins, else the
// "Output is being written to:" path in the tool_result answering the tool_use
// the task's task_started names. A torn or unparsable line is skipped, since
// the log may be mid-append.
func FindTaskOutput(r io.Reader, taskID string) (TaskOutput, error) {
	var out TaskOutput
	id := []byte(taskID)
	var toolUseID, notified string
	resultPaths := map[string]string{}
	br := bufio.NewReader(r)
	for {
		line, readErr := br.ReadBytes('\n')
		if (bytes.Contains(line, id) || bytes.Contains(line, []byte(`"init"`))) && bytes.HasSuffix(line, []byte("\n")) {
			if ev := outEvent(line); ev != nil {
				switch ev := ev.(type) {
				case SystemInit:
					if ev.SessionID != "" && !slices.Contains(out.ClaudeSessionIDs, ev.SessionID) {
						out.ClaudeSessionIDs = append(out.ClaudeSessionIDs, ev.SessionID)
					}
				case System:
					var task struct {
						TaskID     string `json:"task_id"`
						ToolUseID  string `json:"tool_use_id"`
						OutputFile string `json:"output_file"`
					}
					if json.Unmarshal(ev.Raw, &task) != nil || task.TaskID != taskID {
						break
					}
					if ev.Subtype == "task_started" && task.ToolUseID != "" {
						toolUseID = task.ToolUseID
					}
					if ev.Subtype == "task_notification" && task.OutputFile != "" {
						notified = task.OutputFile
					}
				case User:
					for _, b := range ev.Blocks {
						if res, ok := b.(ToolResultBlock); ok {
							if m := outputPathInResult.FindStringSubmatch(resultText(res.Content)); m != nil {
								resultPaths[res.ToolUseID] = m[1]
							}
						}
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return TaskOutput{}, fmt.Errorf("chatproto: read log: %w", readErr)
		}
	}
	out.Path = notified
	if out.Path == "" && toolUseID != "" {
		out.Path = resultPaths[toolUseID]
	}
	return out, nil
}

// outEvent parses a log line written by claude, or returns nil.
func outEvent(line []byte) Event {
	var e Entry
	if json.Unmarshal(line, &e) != nil || e.Dir != DirOut {
		return nil
	}
	ev, err := ParseLine(e.Payload)
	if err != nil {
		return nil
	}
	return ev
}

// resultText is a tool_result's text, whether claude sent a string or blocks.
func resultText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var b bytes.Buffer
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
