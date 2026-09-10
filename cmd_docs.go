package main

import (
	"github.com/spf13/cobra"
)

// newDocTopics returns the reference documentation that ships inside the
// binary, as cobra help topics.
//
// A help topic is a command with no Run: cobra lists it under "Additional help
// topics:" rather than "Available Commands:", and its help renders the Long
// alone, with no usage footer. That is exactly a document reader, without a
// command that has to invent something to do when it is run.
//
// These exist because `gopatch patch --help` used to end by naming a file in
// the repository. Anyone who installed a binary had no such file.
func (cmd *mainCmd) newDocTopics() []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "patches",
			Short: "The patch language, in depth.",
			Long:  _patchesTopicLong,
		},
		{
			Use:   "metavariables",
			Short: "Identifiers vs expressions vs statements.",
			Long:  _metavariablesTopicLong,
		},
		{
			Use:   "examples",
			Short: "Worked example patches.",
			Long:  _examplesTopicLong,
		},
	}
}
