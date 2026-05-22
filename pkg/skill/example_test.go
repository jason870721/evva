package skill_test

import (
	"fmt"

	"github.com/johnny1110/evva/pkg/skill"
)

// ExampleNewRegistry shows the SDK path for downstream apps that want a
// programmatic-only skill catalog: build an empty registry and Add each
// skill with a BodyFunc. Mixed disk+programmatic catalogs start from
// LoadRegistry and call Add for extras.
func ExampleNewRegistry() {
	r := skill.NewRegistry()
	_ = r.Add(skill.SkillMeta{
		Name:        "commit",
		Description: "draft a conventional-commits message from the staged diff",
		BodyFunc: func() (string, error) {
			return "Generate a one-line conventional-commits message.", nil
		},
	})

	body, _ := r.LoadBody("commit")
	fmt.Println("names:", r.Names())
	fmt.Println("body:", body)
	// Output:
	// names: [commit]
	// body: Generate a one-line conventional-commits message.
}

// ExampleRegistry_Add demonstrates the validation rules: name must be
// non-empty, BodyFunc must be set, duplicates are rejected. Add is
// typically called once per skill at host bootstrap before agent.New
// runs.
func ExampleRegistry_Add() {
	r := skill.NewRegistry()
	if err := r.Add(skill.SkillMeta{Name: ""}); err != nil {
		fmt.Println("empty-name:", err != nil)
	}
	if err := r.Add(skill.SkillMeta{Name: "no-body"}); err != nil {
		fmt.Println("no-body:", err != nil)
	}
	_ = r.Add(skill.SkillMeta{
		Name:     "review",
		BodyFunc: func() (string, error) { return "...", nil },
	})
	if err := r.Add(skill.SkillMeta{
		Name:     "review",
		BodyFunc: func() (string, error) { return "...", nil },
	}); err != nil {
		fmt.Println("duplicate:", err != nil)
	}
	// Output:
	// empty-name: true
	// no-body: true
	// duplicate: true
}
