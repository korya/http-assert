package main

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/onsi/gomega"
)

func Test_AssertStatusOK(t *testing.T) {
	g := gomega.NewWithT(t)

	testCases := []struct {
		StatusCode int
		Status     string
		OK         bool
	}{
		{0, "UNKNOWN", false},
		{100, "CONTINUE", false},
		{200, "OK", true},
		{201, "Created", true},
		{204, "No Content", true},
		{299, "Custom OK Response", true},
		{300, "Multiple Choice", false},
		{301, "Moved Permanently", false},
		{307, "Temporary Redirect", false},
		{400, "Bad Request", false},
		{429, "Too Many Requests", false},
		{500, "Internal Server Error", false},
		{914, "Custom Response", false},
	}

	ok := AssertStatusOK()
	nok := AssertStatusNOK()
	for _, tc := range testCases {
		res := &httpResponse{
			Response: &http.Response{
				StatusCode: tc.StatusCode,
				Status:     tc.Status,
			},
		}

		msg := strconv.Itoa(tc.StatusCode)
		if tc.OK {
			g.Expect(ok(res)).To(gomega.BeNil(), msg)
			g.Expect(nok(res)).To(gomega.MatchError(
				fmt.Sprintf("nok: expected NOK, got=%d (%q)", tc.StatusCode, tc.Status),
			), msg)
		} else {
			g.Expect(ok(res)).To(gomega.MatchError(
				fmt.Sprintf("ok: expected OK, got=%d (%q)", tc.StatusCode, tc.Status),
			), msg)
			g.Expect(nok(res)).To(gomega.BeNil(), msg)
		}
	}
}

func Test_AssertStatus(t *testing.T) {
	g := gomega.NewWithT(t)

	testCases := []struct {
		StatusCode int
		Status     string
		OK         bool
	}{
		{0, "UNKNOWN", false},
		{100, "CONTINUE", false},
		{200, "OK", true},
		{201, "Created", true},
		{204, "No Content", true},
		{299, "Custom OK Response", true},
		{300, "Multiple Choice", false},
		{301, "Moved Permanently", false},
		{307, "Temporary Redirect", false},
		{400, "Bad Request", false},
		{429, "Too Many Requests", false},
		{500, "Internal Server Error", false},
		{914, "Custom Response", false},
	}

	a1 := AssertStatus(1)
	a200 := AssertStatus(200)
	a429 := AssertStatus(429)
	for _, tc := range testCases {
		res := &httpResponse{
			Response: &http.Response{
				StatusCode: tc.StatusCode,
				Status:     tc.Status,
			},
		}

		msg := strconv.Itoa(tc.StatusCode)
		g.Expect(a1(res)).To(gomega.MatchError(
			fmt.Sprintf("status: expected 1, got=%d (%q)", tc.StatusCode, tc.Status),
		))
		if tc.StatusCode != 200 {
			g.Expect(a200(res)).To(gomega.MatchError(
				fmt.Sprintf("status: expected 200, got=%d (%q)", tc.StatusCode, tc.Status),
			))
		} else {
			g.Expect(a200(res)).To(gomega.BeNil(), msg)
		}
		if tc.StatusCode != 429 {
			g.Expect(a429(res)).To(gomega.MatchError(
				fmt.Sprintf("status: expected 429, got=%d (%q)", tc.StatusCode, tc.Status),
			))
		} else {
			g.Expect(a429(res)).To(gomega.BeNil(), msg)
		}
	}
}
