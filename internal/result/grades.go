package result

// gradeMeaning is the plain-language reading of each grade letter. It
// describes how hard it is to change provider, not a cost, and it is shared by
// every report format so they never disagree.
var gradeMeaning = map[string]string{
	"A": "Lock-in is minimal: the system relies on portable building blocks, so changing provider is mostly a configuration change.",
	"B": "Lock-in is low: a few services are provider-specific, but they are add-ons rather than the core and each has known alternatives.",
	"C": "Lock-in is moderate: some provider-specific services hold application data or logic, so changing provider is a planned project.",
	"D": "Lock-in is high: provider-specific services are an important part of how the system runs, so changing provider needs a migration plan.",
	"E": "Lock-in is very high: the system is built around one provider's services, including data and logic, so changing provider means a redesign.",
}

// GradeMeaning returns the one-sentence reading of a grade letter, or "" for
// a letter without one.
func GradeMeaning(grade string) string { return gradeMeaning[grade] }
