package marketplacejson

import (
	"strings"
	"testing"
)

func TestMarshalProducesStableOfficialURLSourceShape(t *testing.T) {
	marketplace := validMarketplace()

	first, err := Marshal(marketplace)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	second, err := Marshal(marketplace)
	if err != nil {
		t.Fatalf("second Marshal() error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("Marshal() is not stable:\n%s\n%s", first, second)
	}
	want := `{"name":"ctf-web","owner":{"name":"Security Team"},"plugins":[{"name":"java-scanner","source":{"source":"url","url":"https://marketplace.example/distribution/plugins/0195c54e-84fa-7b63-9fba-bd3f0588cb3a.git","ref":"v1.2.3","sha":"0123456789abcdef0123456789abcdef01234567"}}]}`
	if string(first) != want {
		t.Fatalf("Marshal() = %s, want %s", first, want)
	}
}

func TestValidateRejectsNonReproducibleSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Marketplace)
		want   string
	}{
		{"marketplace name", func(m *Marketplace) { m.Name = "CTF Web" }, "kebab-case"},
		{"owner", func(m *Marketplace) { m.Owner.Name = " " }, "owner name"},
		{"nil plugins", func(m *Marketplace) { m.Plugins = nil }, "must be an array"},
		{"source type", func(m *Marketplace) { m.Plugins[0].Source.Source = "git" }, "source must be"},
		{"URL", func(m *Marketplace) { m.Plugins[0].Source.URL = "" }, "URL is required"},
		{"ref", func(m *Marketplace) { m.Plugins[0].Source.Ref = "" }, "ref is required"},
		{"SHA", func(m *Marketplace) { m.Plugins[0].Source.SHA = "abc" }, "full lowercase"},
		{"duplicate plugin", func(m *Marketplace) { m.Plugins = append(m.Plugins, m.Plugins[0]) }, "duplicated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			marketplace := validMarketplace()
			test.mutate(&marketplace)
			err := Validate(marketplace)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func validMarketplace() Marketplace {
	return Marketplace{
		Name:  "ctf-web",
		Owner: Owner{Name: "Security Team"},
		Plugins: []Plugin{{
			Name: "java-scanner",
			Source: URLSource{
				Source: URLSourceType,
				URL:    "https://marketplace.example/distribution/plugins/0195c54e-84fa-7b63-9fba-bd3f0588cb3a.git",
				Ref:    "v1.2.3",
				SHA:    "0123456789abcdef0123456789abcdef01234567",
			},
		}},
	}
}
