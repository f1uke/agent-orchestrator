package testiny

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Each input is a rich-text value recorded from a real MOB case (test data
// replaced with fake values), trimmed to the nodes the case is about.
func TestRichTextKeepsTheStructure(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", ``, ``},
		{"plain text passes through", "  qa@example.com / fake-password\n", "qa@example.com / fake-password"},
		{"not slate json passes through", `{"note":"x"}`, `{"note":"x"}`},
		{"only an empty paragraph", `{"t":"slate","v":1,"c":[{"t":"p","children":[{"text":""}]}]}`, ``},
		{
			"bullet list",
			`{"t":"slate","v":1,"c":[{"t":"ul","children":[{"t":"li","children":[{"t":"p","children":[{"text":"Logged in to the Main app (Android or iOS)"}]}]},{"t":"li","children":[{"t":"p","children":[{"text":"A chat room exists where a fund disclaimer notice message has been sent"}]}]}]}]}`,
			"- Logged in to the Main app (Android or iOS)\n- A chat room exists where a fund disclaimer notice message has been sent",
		},
		{
			"paragraphs are separated by a blank line and empty ones dropped",
			`{"t":"slate","v":1,"c":[{"t":"p","children":[{"text":"INT"}]},{"children":[{"text":"Advisor: advisor@example.com | fake-password"}],"t":"p"},{"children":[{"text":""}],"t":"p"}]}`,
			"INT\n\nAdvisor: advisor@example.com | fake-password",
		},
		{
			"ordered list keeps its start number",
			`{"t":"slate","v":1,"c":[{"t":"p","children":[{"text":"1. เข้า chat room"}]},{"children":[{"text":"2. รับสายโทรเข้า"}],"t":"p"},{"t":"ol","children":[{"t":"li","children":[{"children":[{"text":"กดวาง"}],"t":"p"}]},{"t":"li","children":[{"t":"p","children":[{"text":"ปิดแอป"}]}]}],"start":3}]}`,
			"1. เข้า chat room\n\n2. รับสายโทรเข้า\n\n3. กดวาง\n4. ปิดแอป",
		},
		{
			"ordered list starts at 1",
			`{"t":"slate","v":1,"c":[{"t":"ol","children":[{"t":"li","children":[{"t":"p","children":[{"text":"Advisor text to customer"}]}]}]}]}`,
			"1. Advisor text to customer",
		},
		{
			"nested list is indented under its item, and marks are plain text",
			`{"t":"slate","v":1,"c":[{"t":"ul","children":[{"t":"li","children":[{"t":"p","children":[{"text":"Environment: "},{"text":"Integration","bold":true}]}]},{"t":"li","children":[{"t":"p","children":[{"text":"Use a test user:"}]},{"t":"ul","children":[{"t":"li","children":[{"t":"p","children":[{"text":"user-a@example.com","code":true},{"text":" / "},{"text":"fake-password","code":true}]}]},{"t":"li","children":[{"t":"p","children":[{"text":"user-b@example.com","code":true}]}]}]}]}]}]}`,
			"- Environment: Integration\n- Use a test user:\n  - user-a@example.com / fake-password\n  - user-b@example.com",
		},
		{
			"ordered item continues under its number",
			`{"t":"slate","v":1,"c":[{"t":"ol","children":[{"t":"li","children":[{"t":"p","children":[{"text":"First"}]},{"t":"p","children":[{"text":"more about it"}]}]}]}]}`,
			"1. First\n   more about it",
		},
		{
			"a link shows its address when the text differs",
			`{"t":"slate","v":1,"c":[{"t":"p","children":[{"text":"Open "},{"t":"a","children":[{"text":"the promotion"}],"url":"https://example.com/promotion"},{"text":" and "},{"t":"a","children":[{"text":"https://example.com/a"}],"url":"https://example.com/a"}]}]}`,
			"Open the promotion (https://example.com/promotion) and https://example.com/a",
		},
		{
			"code block keeps its lines",
			`{"t":"slate","v":1,"c":[{"t":"ol","children":[{"t":"li","children":[{"t":"p","children":[{"text":"Call the API"}]}]}]},{"t":"code","children":[{"text":"curl -X PUT 'https://example.com/send' \\\n  --data '{}'"}]}]}`,
			"1. Call the API\n\ncurl -X PUT 'https://example.com/send' \\\n  --data '{}'",
		},
		{
			"table rows are cell | cell",
			`{"t":"slate","v":1,"c":[{"t":"t","columns":2,"children":[{"t":"tr","rid":"aX07oi","children":[{"t":"td","children":[{"t":"p","children":[{"text":"ย่อหน้าจอ voice call"}]}]},{"t":"td","children":[{"t":"p","children":[{"text":"แสดงหน้า voice call แบบย่อ"}]},{"t":"p","children":[{"text":"ไม่ค้าง"}]}]}]}]}]}`,
			"ย่อหน้าจอ voice call | แสดงหน้า voice call แบบย่อ / ไม่ค้าง",
		},
		{
			"an unknown node falls back to its text",
			`{"t":"slate","v":1,"c":[{"t":"callout","children":[{"t":"p","children":[{"text":"Heads up"}]},{"t":"p","children":[{"text":"twice"}]}]},{"t":"h1","children":[{"text":"Title"}]}]}`,
			"Heads up\ntwice\n\nTitle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := richText(tc.in); got != tc.want {
				t.Fatalf("richText =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestStepsTableReadsEachRowAsActionAndExpected(t *testing.T) {
	in := `{"t":"slate","v":1,"c":[{"t":"t","columns":2,"children":[` +
		`{"t":"tr","rid":"5quWOzIAc5","children":[{"t":"td","children":[{"t":"p","children":[{"text":"Open the Main app and go to Finnomena Chat"}]}]},{"t":"td","children":[{"t":"p","children":[{"text":"Chat list is displayed"}]}]}]},` +
		`{"t":"tr","rid":"F0GRCtMY9d","children":[{"t":"td","children":[{"t":"p","children":[{"text":"Send a link such as "},{"t":"a","children":[{"text":"https://example.com/promotion"}],"url":"https://example.com/promotion"}]}]},{"t":"td","children":[{"t":"ul","children":[{"t":"li","children":[{"t":"p","children":[{"text":"The message shows"}]}]},{"t":"li","children":[{"t":"p","children":[{"text":"The link can be tapped"}]}]}]}]}]},` +
		`{"t":"tr","rid":"e","children":[{"t":"td","children":[{"t":"p","children":[{"text":""}]}]},{"t":"td","children":[{"t":"p","children":[{"text":""}]}]}]},` +
		`{"t":"tr","rid":"x","children":[{"t":"td","children":[{"t":"p","children":[{"text":"Close the app"}]}]}]}` +
		`]}]}`
	want := []domain.TestinyCaseStep{
		{N: 1, Action: "Open the Main app and go to Finnomena Chat", Expected: "Chat list is displayed"},
		{N: 2, Action: "Send a link such as https://example.com/promotion", Expected: "- The message shows\n- The link can be tapped"},
		{N: 3, Action: "Close the app", Expected: ""},
	}
	if got := stepsTable(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("stepsTable =\n%+v\nwant\n%+v", got, want)
	}
}

func TestStepsTableOfNoStepsIsEmpty(t *testing.T) {
	for _, in := range []string{``, `{"t":"slate","v":1,"c":[{"t":"p","children":[{"text":""}]}]}`, `not json`} {
		if got := stepsTable(in); len(got) != 0 {
			t.Fatalf("stepsTable(%q) = %+v, want none", in, got)
		}
	}
}
