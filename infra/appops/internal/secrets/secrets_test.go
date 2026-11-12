package secrets

import "testing"

func TestBuildConnectionURI_EncodesPassword(t *testing.T) {
	got := BuildConnectionURI(ConnectionSettings{Host: "db.example.com", Port: 5432, UserSuffix: ".ref"},
		"app_blog_dev", "p@ss:w/rd#?%", "postgres")
	want := "postgresql://app_blog_dev.ref:p%40ss%3Aw%2Frd%23%3F%25@db.example.com:5432/postgres"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
