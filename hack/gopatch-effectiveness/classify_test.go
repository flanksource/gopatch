package main

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHistoryAnalysis(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "History analysis candidates")
}

var _ = DescribeTable("Go edit candidate discovery",
	func(tool, input string, expected bool) {
		c := call{Tool: tool}
		Expect(json.Unmarshal([]byte(input), &c.Input)).To(Succeed())
		Expect(isGoEdit(c)).To(Equal(expected))
	},
	Entry("Go edit", "Edit", `{"file_path":"pkg/account.go","old_string":"Old","new_string":"New"}`, true),
	Entry("Go file move", "Bash", `{"command":"mv internal/old.go internal/new.go"}`, true),
	Entry("inspection is excluded", "Bash", `{"command":"rg -n 'Old' internal/account.go"}`, false),
	Entry("non-Go edit is excluded", "Edit", `{"file_path":"src/account.ts","old_string":"Old","new_string":"New"}`, false),
	Entry("multiline normalized patch", "Edit", `{"patch":"*** Begin Patch\n*** Update File: pkg/account.go\n@@\n-old\n+new\n*** End Patch"}`, true),
	Entry("normalized mixed-file apply patch", "ApplyPatch", `{"files":[{"path":"ui/view.tsx"},{"path":"pkg/account.go"}],"input":"*** Begin Patch\n*** Update File: pkg/account.go\n@@\n-old\n+new\n*** End Patch"}`, true),
)

var _ = DescribeTable("executed gopatch commands",
	func(script string, n int) {
		commands, err := executedGopatch(script)
		Expect(err).NotTo(HaveOccurred())
		Expect(commands).To(HaveLen(n))
	},
	Entry("search mentions do not execute", `rg -n 'gopatch rename package' README.md`, 0),
	Entry("quoted code is not an invocation", `printf '%s' 'gopatch patch -p a.patch .'`, 0),
	Entry("assignments and absolute binaries", `GOWORK=off /Users/example/go/bin/gopatch rename package old new .`, 1),
	Entry("preview and apply are two invocations", "gopatch patch -d -p a.patch .\ngopatch patch -p a.patch .", 2),
)

var _ = Describe("tool result envelopes", func() {
	It("reads nested execution metadata without treating output text as metadata", func() {
		e := resultEnvelope(`{"value":{"exit_code":0,"wall_time_seconds":0.5,"output":"error is a variable name"}}`)
		Expect(e.Exits).To(Equal([]int{0}))
		Expect(e.Seconds).To(Equal([]float64{0.5}))
		Expect(resultStatus(nativeRow{}, e)).To(Equal("succeeded"))
	})
	It("retains failures across concatenated parallel results", func() {
		e := resultEnvelope(`{"value":{"exit_code":0,"output":"first"}}{"value":{"exit_code":1,"output":"second"}}`)
		Expect(e.Exits).To(Equal([]int{0, 1}))
		Expect(resultStatus(nativeRow{}, e)).To(Equal("failed"))
	})
	It("keeps a yielded process pending", func() {
		e := resultEnvelope(`{"session_id":1234,"wall_time_seconds":1,"output":""}`)
		Expect(resultStatus(nativeRow{ResultFound: true}, e)).To(Equal("pending"))
	})
	It("does not invent success for missing results", func() { Expect(resultStatus(nativeRow{}, resultEnvelope(""))).To(Equal("unknown")) })
})

var _ = Describe("summary integrity", func() {
	It("averages the two central values for an even sample median", func() { Expect(distribution([]float64{8, 2, 6, 4})["median"]).To(Equal(5.0)) })
	It("does not attribute a neighboring patch's error to this patch", func() {
		Expect(diagnosticMatchesCommand(`load patch "other.patch": parse failed`, "gopatch patch -p selected.patch x.go")).To(BeFalse())
	})
	It("excludes same-line test commands from isolated gopatch timing", func() {
		v := invocation{candidate: candidate{call: call{Tool: "Bash", Input: map[string]json.RawMessage{"command": json.RawMessage(`"gopatch patch -p a.patch x.go; go test ./..."`)}}}}
		Expect(isolatedGopatch(v)).To(BeFalse())
	})
})
