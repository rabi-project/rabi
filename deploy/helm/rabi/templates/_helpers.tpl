{{/* SPDX-License-Identifier: Apache-2.0 */}}
{{- define "rabi.fullname" -}}
{{- .Release.Name -}}
{{- end -}}

{{- define "rabi.labels" -}}
app.kubernetes.io/name: rabi
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "rabi.dbUrl" -}}
{{- if .Values.postgres.enabled -}}
postgres://{{ .Values.postgres.user }}:{{ .Values.postgres.password }}@{{ include "rabi.fullname" . }}-postgres:5432/{{ .Values.postgres.database }}?sslmode=disable
{{- else -}}
{{- required "externalDatabaseUrl is required when postgres.enabled=false" .Values.externalDatabaseUrl -}}
{{- end -}}
{{- end -}}
