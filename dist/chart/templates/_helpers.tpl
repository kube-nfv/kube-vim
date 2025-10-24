{{/*
Base name - always returns "kube-vim"
*/}}
{{- define "kube-vim.name" -}}
kube-vim
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kube-vim.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kube-vim.labels" -}}
helm.sh/chart: {{ include "kube-vim.chart" . }}
{{ include "kube-vim.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kube-vim.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-vim.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kube-vim.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default "kube-vim" .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
VIM component name - always returns "kube-vim"
*/}}
{{- define "kube-vim.vim.name" -}}
kube-vim
{{- end }}

{{/*
VIM component labels
*/}}
{{- define "kube-vim.vim.labels" -}}
helm.sh/chart: {{ include "kube-vim.chart" . }}
{{ include "kube-vim.vim.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: vim
{{- end }}

{{/*
VIM selector labels
*/}}
{{- define "kube-vim.vim.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-vim.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: vim
{{- end }}

{{/*
Gateway component name - always returns "kube-vim-gateway"
*/}}
{{- define "kube-vim.gateway.name" -}}
kube-vim-gateway
{{- end }}

{{/*
Gateway component labels
*/}}
{{- define "kube-vim.gateway.labels" -}}
helm.sh/chart: {{ include "kube-vim.chart" . }}
{{ include "kube-vim.gateway.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: gateway
{{- end }}

{{/*
Gateway selector labels
*/}}
{{- define "kube-vim.gateway.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kube-vim.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: gateway
{{- end }}

{{/*
Return the proper image name for VIM
*/}}
{{- define "kube-vim.vim.image" -}}
{{- $registry := .Values.global.image.registry -}}
{{- $repository := .Values.vim.image.repository -}}
{{- $tag := .Values.vim.image.tag | default .Values.global.image.tag -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- end }}

{{/*
Return the proper image name for Gateway
*/}}
{{- define "kube-vim.gateway.image" -}}
{{- $registry := .Values.global.image.registry -}}
{{- $repository := .Values.gateway.image.repository -}}
{{- $tag := .Values.gateway.image.tag | default .Values.global.image.tag -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- end }}

{{/*
Return the proper image pull policy for VIM
*/}}
{{- define "kube-vim.vim.imagePullPolicy" -}}
{{- if .Values.vim.image.pullPolicy }}
{{- .Values.vim.image.pullPolicy }}
{{- else }}
{{- .Values.global.image.pullPolicy }}
{{- end }}
{{- end }}

{{/*
Return the proper image pull policy for Gateway
*/}}
{{- define "kube-vim.gateway.imagePullPolicy" -}}
{{- if .Values.gateway.image.pullPolicy }}
{{- .Values.gateway.image.pullPolicy }}
{{- else }}
{{- .Values.global.image.pullPolicy }}
{{- end }}
{{- end }}

{{/*
Return the proper image pull secrets
*/}}
{{- define "kube-vim.imagePullSecrets" -}}
{{- if .Values.global.image.pullSecrets }}
{{- range .Values.global.image.pullSecrets }}
- name: {{ . }}
{{- end }}
{{- end }}
{{- end }}
