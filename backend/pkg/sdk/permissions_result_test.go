package sdk

import (
	"slices"
	"testing"

	jsonv2 "encoding/json/v2"
)

func TestPermissionsResultJSON(t *testing.T) {
	b, err := jsonv2.Marshal(PermissionsResult{Granted: []Permission{"entities.read"}, Requested: []string{"entities.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"granted":["entities.read"],"requested":["entities.read"]}`; got != want {
		t.Errorf("without mail = %s, want %s", got, want)
	}
	b, err = jsonv2.Marshal(PermissionsResult{Granted: []Permission{"mail.send"}, Requested: []string{"mail.send"}, Mail: &MailQuota{RecipientsPerHour: 360}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"granted":["mail.send"],"requested":["mail.send"],"mail":{"recipients_per_hour":360}}`; got != want {
		t.Errorf("with mail = %s, want %s", got, want)
	}
}

func TestPermissionsResultUnmarshal(t *testing.T) {
	var r PermissionsResult
	if err := jsonv2.Unmarshal([]byte(`{"granted":["mail.send"],"requested":["mail.send","site.scripts"],"mail":{"recipients_per_hour":3600}}`), &r); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Granted, []Permission{"mail.send"}) || len(r.Requested) != 2 || r.Mail == nil || r.Mail.RecipientsPerHour != 3600 {
		t.Errorf("got %+v", r)
	}
	var none PermissionsResult
	if err := jsonv2.Unmarshal([]byte(`{"granted":[],"requested":[]}`), &none); err != nil || none.Mail != nil {
		t.Errorf("no mail: %+v, %v", none, err)
	}
}
