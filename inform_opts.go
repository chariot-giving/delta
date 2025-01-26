package delta

import "regexp"

// Regular expression to which the format of tags must comply. Mainly, no
// special characters, and with hyphens in the middle.
//
// A key property here (in case this is relaxed in the future) is that commas
// must never be allowed because they're used as a delimiter during batch job
// insertion for the `riverdatabasesql` driver.
var tagRE = regexp.MustCompile(`\A[\w][\w\-]+[\w]\z`)

// InformOpts are optional settings for a new resource which can be provided at resource
// information time. These will override any default InformOpts settings provided
// by ObjectWithInformOpts, as well as any global defaults.
type InformOpts struct {
	// Namespace is the namespace to use for resource isolation.
	//
	// Defaults to NamespaceDefault.
	Namespace string
	// Metadata is a JSON object blob of arbitrary data that will be stored with
	// the resource. Users should not overwrite or remove anything stored in this
	// field by Delta.
	Metadata []byte

	// Tags are an arbitrary list of keywords to add to the resource. They have no
	// functional behavior and are meant entirely as a user-specified construct
	// to help group and categorize resources.
	//
	// Tags should conform to the regex `\A[\w][\w\-]+[\w]\z` and be a maximum
	// of 255 characters long. No special characters are allowed.
	//
	// If tags are specified from both a resource override and from options on
	// Inform, the latter takes precedence. Tags are not merged.
	Tags []string
}
