{{- define "ods-foundation.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "ods-foundation.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "ods-foundation.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "ods-foundation.labels" -}}
app.kubernetes.io/name: {{ include "ods-foundation.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "ods-foundation.topologySpreadConstraints" -}}
{{- $root := .root -}}
{{- $component := .component -}}
{{- if $root.Values.topologySpread.enabled }}
topologySpreadConstraints:
  {{- range $constraint := $root.Values.topologySpread.constraints }}
  - maxSkew: {{ $constraint.maxSkew }}
    topologyKey: {{ $constraint.topologyKey | quote }}
    whenUnsatisfiable: {{ $constraint.whenUnsatisfiable }}
    labelSelector:
      matchLabels:
        app.kubernetes.io/instance: {{ $root.Release.Name }}
        app.kubernetes.io/component: {{ $component }}
  {{- end }}
{{- end }}
{{- end -}}
