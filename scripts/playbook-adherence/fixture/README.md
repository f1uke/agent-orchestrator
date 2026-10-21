# tally

Turns a timesheet (JSON lines of `{ "client", "project", "date", "duration", "note" }`)
into per-client invoices and summary reports.

    tally invoice timesheet.jsonl --client northwind --rate 95
    tally report timesheet.jsonl

Durations are written the way people type them: `45m`, `2h`, `1h30m`, `90s`.
