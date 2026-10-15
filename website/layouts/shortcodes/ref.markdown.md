{{/*
  Markdown-output variant of the built-in `ref` shortcode.

  Only this output format is overridden — layouts/shortcodes/ref.html does NOT
  exist, so the HTML build keeps using Hugo's internal `ref` implementation
  unchanged everywhere on the site.

  For targets inside the user-guide section (which also render a "markdown"
  output format), link to the absolute .md permalink so markdown-to-markdown
  cross-references stay in markdown. For any other target, fall back to the
  normal absolute HTML permalink (matching the built-in ref's behavior).
*/}}
{{- $raw := .Get "path" | default (.Get 0) -}}
{{- $parts := split $raw "#" -}}
{{- $target := index $parts 0 -}}
{{- $fragment := "" -}}
{{- if gt (len $parts) 1 -}}{{- $fragment = delimit (after 1 $parts) "#" -}}{{- end -}}
{{- $p := .Page.GetPage $target -}}
{{- with $p -}}
  {{- $md := .OutputFormats.Get "markdown" -}}
  {{- if and $md (in .RelPermalink "/user-guide/") -}}
    {{- $md.Permalink -}}
  {{- else -}}
    {{- .Permalink -}}
  {{- end -}}
  {{- with $fragment }}#{{ . }}{{ end -}}
{{- else -}}
  {{- errorf "ref: page not found: %s" $target -}}
{{- end -}}
