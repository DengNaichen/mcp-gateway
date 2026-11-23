package registry

type Skill struct {
	ID              string   `json:"id"`
	Name            string   `json:"name,omitempty"`
	Enabled         bool     `json:"enabled"`
	AllowedRepos    []string `json:"allowed_repos"`
	AllowedServices []string `json:"allowed_services"`
	UpdatedAt       string   `json:"updated_at"`
}
