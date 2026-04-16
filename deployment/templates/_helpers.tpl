{{/*
Standard Helm labels applied to every resource.
*/}}
{{- define "fusion-forge.labels" -}}
app.kubernetes.io/name: fusion-forge
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/*
Kubernetes Service name for the REST server.
*/}}
{{- define "fusion-forge.serverServiceName" -}}
{{- .Values.server.serviceName | default "fusion-forge" }}
{{- end }}

{{/*
ServiceAccount name for the REST server.
*/}}
{{- define "fusion-forge.serverSAName" -}}
fusion-forge-server
{{- end }}

{{/*
ServiceAccount name for the operator.
*/}}
{{- define "fusion-forge.operatorSAName" -}}
fusion-forge-operator
{{- end }}

{{/*
ServiceAccount name for builder pods.
*/}}
{{- define "fusion-forge.builderSAName" -}}
fusion-forge-builder
{{- end }}

{{/*
PostgreSQL StatefulSet / Service name.
*/}}
{{- define "fusion-forge.postgresqlName" -}}
{{ .Release.Name }}-postgresql
{{- end }}

{{/*
Database host — embedded StatefulSet FQDN or external host.
*/}}
{{- define "fusion-forge.dbHost" -}}
{{- if .Values.postgresql.enabled -}}
{{- include "fusion-forge.postgresqlName" . }}
{{- else -}}
{{- .Values.postgresql.external.host -}}
{{- end -}}
{{- end }}

{{/*
Database port.
*/}}
{{- define "fusion-forge.dbPort" -}}
{{- if .Values.postgresql.enabled -}}
5432
{{- else -}}
{{- .Values.postgresql.external.port | default 5432 -}}
{{- end -}}
{{- end }}

{{/*
Database name.
*/}}
{{- define "fusion-forge.dbName" -}}
{{- if .Values.postgresql.enabled -}}
{{- .Values.postgresql.auth.database -}}
{{- else -}}
{{- .Values.postgresql.external.database -}}
{{- end -}}
{{- end }}

{{/*
Database username.
*/}}
{{- define "fusion-forge.dbUsername" -}}
{{- if .Values.postgresql.enabled -}}
{{- .Values.postgresql.auth.username -}}
{{- else -}}
{{- .Values.postgresql.external.username -}}
{{- end -}}
{{- end }}

{{/*
Secret name that contains the database password (key: "password").

Resolution order:
  1. Bundled postgresql + existingSecret set  → user-provided secret
  2. Bundled postgresql, no existingSecret    → chart-managed secret (<release>-postgresql-secret)
  3. External DB + existingSecret set         → user-provided secret
  4. External DB, no existingSecret           → chart-managed secret (<release>-db-secret)
*/}}
{{- define "fusion-forge.dbSecretName" -}}
{{- if .Values.postgresql.enabled -}}
  {{- if .Values.postgresql.auth.existingSecret -}}
    {{- .Values.postgresql.auth.existingSecret -}}
  {{- else -}}
    {{- printf "%s-postgresql-secret" .Release.Name -}}
  {{- end -}}
{{- else -}}
  {{- if .Values.postgresql.external.existingSecret -}}
    {{- .Values.postgresql.external.existingSecret -}}
  {{- else -}}
    {{- printf "%s-db-secret" .Release.Name -}}
  {{- end -}}
{{- end -}}
{{- end }}
