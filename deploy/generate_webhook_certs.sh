#!/usr/bin/env bash
#
# Generate a dedicated CA + server certificate for the webhook Service,
# create/update the TLS Secret, and patch the MutatingWebhookConfiguration caBundle.
#
# Default SANs:
#   node-startup-webhook.kube-system.svc
#   node-startup-webhook.kube-system.svc.cluster.local
#
# Requirements:
#   - openssl
#   - kubectl (cluster context pointing to target cluster)
#
# Usage:
#   ./scripts/generate_webhook_certs.sh \
#       --namespace kube-system \
#       --service node-startup-webhook \
#       --secret node-startup-webhook-tls \
#       --webhook node-startup-taint \
#       [--force]
#
# If --force is set existing key/cert files are overwritten and Secret re-created.
#
set -euo pipefail

NAMESPACE="kube-system"
SERVICE="node-startup-webhook"
SECRET="node-startup-webhook-tls"
WEBHOOK_CFG="node-startup-taint"
FORCE=0
OUTDIR="certs"
DAYS=3650

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --service) SERVICE="$2"; shift 2 ;;
    --secret) SECRET="$2"; shift 2 ;;
    --webhook) WEBHOOK_CFG="$2"; shift 2 ;;
    --outdir) OUTDIR="$2"; shift 2 ;;
    --days) DAYS="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

mkdir -p "${OUTDIR}"
cd "${OUTDIR}"

CA_KEY="ca.key"
CA_CRT="ca.crt"
SRV_KEY="server.key"
SRV_CSR="server.csr"
SRV_CRT="server.crt"
OPENSSL_CNF="server.cnf"

echo "==> Configuration"
echo "Namespace:   ${NAMESPACE}"
echo "Service:     ${SERVICE}"
echo "Secret:      ${SECRET}"
echo "WebhookCfg:  ${WEBHOOK_CFG}"
echo "Output dir:  ${PWD}"
echo

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl not found in PATH" >&2
  exit 1
fi
if ! command -v openssl >/dev/null 2>&1; then
  echo "openssl not found in PATH" >&2
  exit 1
fi

if [[ ${FORCE} -eq 1 ]]; then
  rm -f "${CA_KEY}" "${CA_CRT}" "${SRV_KEY}" "${SRV_CSR}" "${SRV_CRT}" "${OPENSSL_CNF}"
fi

if [[ -f "${CA_CRT}" && -f "${CA_KEY}" ]]; then
  echo "CA already exists (use --force to overwrite)"
else
  echo "==> Generating CA"
  openssl req -x509 -new -nodes -newkey rsa:2048 -days "${DAYS}" \
    -subj "/CN=${SERVICE}-ca" \
    -keyout "${CA_KEY}" -out "${CA_CRT}"
fi

cat > "${OPENSSL_CNF}" <<EOF
[req]
prompt = no
distinguished_name = dn
req_extensions = v3_req

[dn]
CN = ${SERVICE}.${NAMESPACE}.svc

[v3_req]
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alts

[alts]
DNS.1 = ${SERVICE}.${NAMESPACE}.svc
DNS.2 = ${SERVICE}.${NAMESPACE}.svc.cluster.local
EOF

if [[ -f "${SRV_CRT}" && ${FORCE} -ne 1 ]]; then
  echo "Server cert already exists (use --force to regenerate)"
else
  echo "==> Generating server key + CSR"
  openssl req -new -newkey rsa:2048 -nodes \
    -keyout "${SRV_KEY}" -out "${SRV_CSR}" -config "${OPENSSL_CNF}"

  echo "==> Signing server certificate with CA"
  openssl x509 -req -in "${SRV_CSR}" -CA "${CA_CRT}" -CAkey "${CA_KEY}" -CAcreateserial \
    -out "${SRV_CRT}" -days "${DAYS}" -extensions v3_req -extfile "${OPENSSL_CNF}"
fi

echo "==> Creating / updating TLS Secret ${SECRET}"
set +e
kubectl -n "${NAMESPACE}" get secret "${SECRET}" >/dev/null 2>&1
SECRET_EXISTS=$?
set -e
if [[ ${SECRET_EXISTS} -eq 0 && ${FORCE} -eq 1 ]]; then
  kubectl -n "${NAMESPACE}" delete secret "${SECRET}"
  SECRET_EXISTS=1
fi
if [[ ${SECRET_EXISTS} -ne 0 ]]; then
  kubectl -n "${NAMESPACE}" create secret tls "${SECRET}" \
    --cert="${SRV_CRT}" --key="${SRV_KEY}"
else
  echo "Secret exists (use --force to recreate)"
fi

echo "==> Generating base64 CA bundle"
# portable base64 (Linux: -w0, macOS: no -w; fallback)
if base64 -w0 < /dev/null >/dev/null 2>&1; then
  CA_BUNDLE=$(base64 -w0 < "${CA_CRT}")
else
  CA_BUNDLE=$(base64 < "${CA_CRT}" | tr -d '\n')
fi
echo "CA bundle length: ${#CA_BUNDLE}"

echo "==> Patching MutatingWebhookConfiguration ${WEBHOOK_CFG}"
PATCH="[ {\"op\":\"replace\",\"path\":\"/webhooks/0/clientConfig/caBundle\",\"value\":\"${CA_BUNDLE}\"} ]"
if ! kubectl get mutatingwebhookconfiguration "${WEBHOOK_CFG}" >/dev/null 2>&1; then
  echo "WARNING: MutatingWebhookConfiguration ${WEBHOOK_CFG} not found. Apply your manifest then re-run patch:"
  echo "kubectl patch mutatingwebhookconfiguration ${WEBHOOK_CFG} --type='json' -p='${PATCH}'"
else
  kubectl patch mutatingwebhookconfiguration "${WEBHOOK_CFG}" --type='json' -p="${PATCH}"
fi

echo
echo "==> Done"
echo "Summary:"
echo "  CA cert:      ${OUTDIR}/${CA_CRT}"
echo "  Server cert:  ${OUTDIR}/${SRV_CRT}"
echo "  Server key:   ${OUTDIR}/${SRV_KEY}"
echo "  Secret:       ${SECRET} (namespace: ${NAMESPACE})"
echo "  Webhook cfg:  ${WEBHOOK_CFG} patched (if existed)"
echo
echo "If needed, update caBundle in manifest [deploy/deployment.yaml] manually with:"
echo "  ${CA_BUNDLE}"