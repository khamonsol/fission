{{- define "fissionFunction.roles" }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ .Release.Name }}-fission-fetcher
  namespace: {{ .namespace }}
rules:
- apiGroups:
  - ""
  resources:
  - configmaps
  - secrets
  verbs:
  - get
- apiGroups:
  - ""
  resources:
  - pods
  verbs:
  - list
- apiGroups:
  - fission.io
  resources:
  - packages
  verbs:
  - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ .Release.Name }}-fission-builder
  namespace: {{ .namespace }}
rules:
- apiGroups:
  - fission.io
  resources:
  - packages
  verbs:
  - get
- apiGroups:
  - ""
  resources:
  - configmaps
  - secrets
  verbs:
  - get
{{- end -}}

{{- define "fissionFunction.rolebindings" }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ .Release.Name }}-fission-fetcher
  namespace: {{ .namespace }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ .Release.Name }}-fission-fetcher
subjects:
  - kind: ServiceAccount
    name: fission-fetcher
    namespace: {{template "fission-function-ns" . }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ .Release.Name }}-fission-builder
  namespace: {{ .namespace }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ .Release.Name }}-fission-builder
subjects:
  - kind: ServiceAccount
    name: fission-builder
    namespace: {{ template "fission-builder-ns" . }}
{{- end -}}

{{- define "functionPod.roles" }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ .Release.Name }}-secret-configmap-getter
  namespace: {{ .namespace }}
rules:
- apiGroups:
  - ""
  resources:
  - secrets
  - configmaps
  verbs:
  - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ .Release.Name }}-package-getter
  namespace: {{ .namespace }}
rules:
- apiGroups:
  - fission.io
  resources:
  - packages
  verbs:
  - get
{{- end -}}
