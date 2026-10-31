{{- define "gearbox-server.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gearbox-server.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "gearbox-server.labels" -}}
app.kubernetes.io/name: {{ include "gearbox-server.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "gearbox-server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gearbox-server.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "gearbox-server.secretName" -}}
{{- default (include "gearbox-server.fullname" .) .Values.secrets.existingSecret -}}
{{- end -}}

{{- define "gearbox-server.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "gearbox-server.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* True when a secret key is available: existing secret (assumed to hold it) or a value to create. */}}
{{- define "gearbox-server.hasSecret" -}}
{{- if or .root.Values.secrets.existingSecret (index .root.Values.secrets .value) -}}true{{- end -}}
{{- end -}}
