package releasetrain

// ExampleConfigJSON is the fictional template `ao train config init` writes; every id in it is made up.
const ExampleConfigJSON = `{
  "timezone": "Asia/Bangkok",
  "historyFrom": "2026-04-01",
  "ageOutDays": 7,
  "fixWrites": "off",
  "gitlabHost": "gitlab.example.com",
  "jira": { "project": "DEMO", "techRequestProject": "DEMOREQ", "forceUpdateSummary": "force update" },
  "confluence": { "space": "DEMOSPACE", "trainPageTitle": "Release train", "freezeCell": "Code freeze", "publishCell": "Store release" },
  "families": [
    { "id": "demo", "label": "Demo", "trainLabel": "Release Train", "apps": ["di", "da"] }
  ],
  "apps": [
    { "id": "di", "name": "Demo iOS", "platform": "ios", "gitlabProject": 1001, "jiraVersionPrefix": "iOS" },
    { "id": "da", "name": "Demo Android", "platform": "android", "gitlabProject": 1002, "jiraVersionPrefix": "Android" }
  ],
  "boards": [
    { "id": 42, "key": "DEMO", "ownsSprintBranch": true },
    { "id": 43, "key": "OTHER", "ownsSprintBranch": false }
  ],
  "mobile": { "projects": ["DEMO"], "subtaskPattern": "^(iOS|ADR|Android|Mobile)\\b" }
}
`
