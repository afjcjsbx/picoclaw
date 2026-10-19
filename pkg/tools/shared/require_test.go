package toolshared

import "testing"

func TestRequireStringArg(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		want    string
		wantErr string
	}{
		{name: "present", args: map[string]any{"path": "/tmp/a.txt"}, want: "/tmp/a.txt"},
		{name: "padded is trimmed", args: map[string]any{"path": " /tmp/a.txt "}, want: "/tmp/a.txt"},
		{name: "absent", args: map[string]any{}, wantErr: "path is required"},
		{name: "blank", args: map[string]any{"path": "  "}, wantErr: "path is required"},
		{name: "empty", args: map[string]any{"path": ""}, wantErr: "path is required"},
		{name: "wrong type", args: map[string]any{"path": 7}, wantErr: "path is required"},
		{name: "null", args: map[string]any{"path": nil}, wantErr: "path is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RequireStringArg(tc.args, "path")
			if (err != nil) != (tc.wantErr != "") {
				t.Fatalf("RequireStringArg() error = %v, want %q", err, tc.wantErr)
			}
			if err != nil {
				if err.Error() != tc.wantErr {
					t.Errorf("message = %q, want %q", err.Error(), tc.wantErr)
				}
				return
			}
			if got != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequireNonEmptyStringArg(t *testing.T) {
	got, err := RequireNonEmptyStringArg(map[string]any{"task": " do it "}, "task")
	if err != nil {
		t.Fatalf("RequireNonEmptyStringArg() error = %v", err)
	}
	if got != "do it" {
		t.Errorf("value = %q, want %q", got, "do it")
	}

	_, err = RequireNonEmptyStringArg(map[string]any{"task": ""}, "task")
	if err == nil {
		t.Fatal("RequireNonEmptyStringArg() accepted an empty argument")
	}
	if want := "task is required and must be a non-empty string"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
