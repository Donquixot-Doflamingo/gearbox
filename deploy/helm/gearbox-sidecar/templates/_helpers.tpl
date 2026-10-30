{{/*
gearbox-sidecar library helpers. Every helper takes the sidecar settings dict
(usually .Values.gearbox of the including chart) as its context.

  securityContext:  {{- include "gearbox-sidecar.podSecurityContext" .Values.gearbox | nindent 8 }}
  initContainers:   {{- include "gearbox-sidecar.container" .Values.gearbox | nindent 8 }}
  containers[app].env:          {{- include "gearbox-sidecar.appEnv" .Values.gearbox | nindent 12 }}
  containers[app].volumeMounts: {{- include "gearbox-sidecar.appVolumeMounts" .Values.gearbox | nindent 12 }}
  volumes:          {{- include "gearbox-sidecar.volumes" .Values.gearbox | nindent 8 }}

With nativeSidecar: false, include "gearbox-sidecar.container" under containers instead.
*/}}

{{- define "gearbox-sidecar.defaults" -}}
image:
  repository: gearbox-sidecar
  tag: "1.0.0"
  pullPolicy: IfNotPresent
app: ""
env: ""
serverUrl: ""
credentialSecret: ""
credentialKey: credential
trustedKeysConfigMap: gearbox-trusted-keys
trustedKeysKey: keys.txt
fsGroup: 2000
nativeSidecar: true
metricsPort: 9464
logSink: server
resources:
  requests:
    cpu: 500m
    memory: 128Mi
  limits:
    memory: 512Mi
extraEnv: []
{{- end -}}

{{/* Settings merged over the defaults, as YAML. */}}
{{- define "gearbox-sidecar.values" -}}
{{- $defaults := fromYaml (include "gearbox-sidecar.defaults" .) -}}
{{- $given := deepCopy (default (dict) .) -}}
{{- toYaml (mustMergeOverwrite $defaults $given) -}}
{{- end -}}

{{- define "gearbox-sidecar.podSecurityContext" -}}
{{- $v := fromYaml (include "gearbox-sidecar.values" .) -}}
fsGroup: {{ $v.fsGroup }}
{{- end -}}

{{- define "gearbox-sidecar.container" -}}
{{- $v := fromYaml (include "gearbox-sidecar.values" .) -}}
{{- $_ := required "gearbox.app is required" $v.app -}}
{{- $_ = required "gearbox.env is required" $v.env -}}
- name: gearbox
  image: "{{ $v.image.repository }}:{{ $v.image.tag }}"
  imagePullPolicy: {{ $v.image.pullPolicy }}
  {{- if $v.nativeSidecar }}
  restartPolicy: Always
  {{- end }}
  env:
    - name: GEARBOX_APP
      value: {{ $v.app | quote }}
    - name: GEARBOX_ENV
      value: {{ $v.env | quote }}
    {{- if $v.serverUrl }}
    - name: GEARBOX_SERVER_URL
      value: {{ $v.serverUrl | quote }}
    {{- end }}
    {{- if $v.credentialSecret }}
    - name: GEARBOX_CREDENTIAL_FILE
      value: /etc/gearbox/credential
    {{- end }}
    - name: GEARBOX_TRUSTED_KEYS_FILE
      value: /etc/gearbox-keys/keys.txt
    - name: GEARBOX_SOCKET
      value: /var/run/gearbox/engine.sock
    - name: GEARBOX_CACHE_DIR
      value: /var/cache/gearbox
    - name: GEARBOX_METRICS_ADDR
      value: {{ printf ":%v" $v.metricsPort | quote }}
    - name: GEARBOX_LOG_SINK
      value: {{ $v.logSink | quote }}
    - name: GEARBOX_SIDECAR_ID
      valueFrom:
        fieldRef:
          fieldPath: metadata.name
    - name: GEARBOX_NODE_NAME
      valueFrom:
        fieldRef:
          fieldPath: spec.nodeName
    - name: GOMAXPROCS
      valueFrom:
        resourceFieldRef:
          resource: requests.cpu
          divisor: "1"
    {{- with $v.extraEnv }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  ports:
    - name: gbx-metrics
      containerPort: {{ $v.metricsPort }}
  startupProbe:
    httpGet:
      path: /readyz
      port: gbx-metrics
    periodSeconds: 1
    failureThreshold: 60
  readinessProbe:
    httpGet:
      path: /readyz
      port: gbx-metrics
    periodSeconds: 5
  livenessProbe:
    httpGet:
      path: /livez
      port: gbx-metrics
    periodSeconds: 10
    failureThreshold: 3
  resources:
    {{- toYaml $v.resources | nindent 4 }}
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
    capabilities:
      drop: [ALL]
  volumeMounts:
    - name: gearbox-socket
      mountPath: /var/run/gearbox
    - name: gearbox-cache
      mountPath: /var/cache/gearbox
    {{- if $v.credentialSecret }}
    - name: gearbox-credential
      mountPath: /etc/gearbox
      readOnly: true
    {{- end }}
    - name: gearbox-keys
      mountPath: /etc/gearbox-keys
      readOnly: true
{{- end -}}

{{- define "gearbox-sidecar.volumes" -}}
{{- $v := fromYaml (include "gearbox-sidecar.values" .) -}}
- name: gearbox-socket
  emptyDir: {}
- name: gearbox-cache
  emptyDir: {}
{{- if $v.credentialSecret }}
- name: gearbox-credential
  secret:
    secretName: {{ $v.credentialSecret }}
    items:
      - key: {{ $v.credentialKey }}
        path: credential
{{- end }}
- name: gearbox-keys
  configMap:
    name: {{ $v.trustedKeysConfigMap }}
    items:
      - key: {{ $v.trustedKeysKey }}
        path: keys.txt
{{- end -}}

{{- define "gearbox-sidecar.appEnv" -}}
- name: GEARBOX_TARGET
  value: unix:///var/run/gearbox/engine.sock
{{- end -}}

{{- define "gearbox-sidecar.appVolumeMounts" -}}
- name: gearbox-socket
  mountPath: /var/run/gearbox
{{- end -}}
