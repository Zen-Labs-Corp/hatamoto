// Entry module of the AI analytics pages ({{define "modules"}}@hm/aia{{end}}).
// The shell boots only top-level modules as script tags (modules in a folder are
// imported by their screen module), so this pulls in the two folder modules the
// pages use: page (draws the charts over the server-rendered tables) and policy
// (the robots pending bar; it does nothing on a page without the form).
import "@hm/aia/page";
import "@hm/aia/policy";
