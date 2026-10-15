{{- $base := strings.TrimSuffix "/" site.BaseURL -}}
{{- $body := replaceRE `\]\(/([^/])` (printf "](%s/$1" $base) .RenderShortcodes -}}
# {{ .Title }}

{{ with .Description }}{{ . }}

{{ end -}}
{{ $body }}
