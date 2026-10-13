package index

import (
	"testing"

	"github.com/mxcd/mailotron/internal/imapclient"
)

func TestExcluded(t *testing.T) {
	ex := []string{"Deleted Messages", "trash", "Archive/Old"}
	cases := []struct {
		name, delim string
		want        bool
	}{
		{"INBOX.Deleted Messages", ".", true},
		{"INBOX.Trash", ".", true},
		{"Trash", "/", true},
		{"Archive/Old", "/", true},
		{"INBOX.Sent", ".", false},
		{"INBOX.Trashcan", ".", false},
		{"INBOX", "", false},
	}
	for _, c := range cases {
		if got := Excluded(imapclient.Mailbox{Name: c.name, Delimiter: c.delim}, ex); got != c.want {
			t.Errorf("Excluded(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
