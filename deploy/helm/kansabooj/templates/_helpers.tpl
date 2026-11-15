{{- define "kansabooj.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kansabooj.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "kansabooj.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kansabooj.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Labels de toutes les ressources : standard + commonLabels. */}}
{{- define "kansabooj.labels" -}}
{{ include "kansabooj.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/part-of: kansabooj
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/*
Métadonnées d'une ressource : labels communs + labels propres, annotations communes + annotations propres.
Usage : include "kansabooj.metadata" (dict "ctx" $ "name" "..." "labels" .Values.x.labels "annotations" .Values.x.annotations)
*/}}
{{- define "kansabooj.metadata" -}}
name: {{ .name }}
labels:
  {{- include "kansabooj.labels" .ctx | nindent 2 }}
  {{- with .labels }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{- $annotations := merge (deepCopy (default (dict) .annotations)) (deepCopy (default (dict) .ctx.Values.commonAnnotations)) }}
{{- with $annotations }}
annotations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}

{{- define "kansabooj.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "kansabooj.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "kansabooj.configMapName" -}}
{{- default (printf "%s-config" (include "kansabooj.fullname" .)) .Values.config.existingConfigMap -}}
{{- end -}}

{{- define "kansabooj.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
{{- end -}}

{{/* Variables d'accès à la base, communes au Deployment et au Job de migration (hors URL). */}}
{{- define "kansabooj.dbPasswordEnv" -}}
- name: KANSABOOJ_DB_PASSWORD   # référencé par database.password: ${KANSABOOJ_DB_PASSWORD}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.database.config.passwordSecret.name }}
      key: {{ .Values.database.config.passwordSecret.key }}
{{- end -}}
