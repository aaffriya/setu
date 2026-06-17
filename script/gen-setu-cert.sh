#!/usr/bin/env bash
# gen-setu-cert.sh
# -----------------------------------------------------------------------------
# Local CA + leaf cert so browsers treat Setu's LAN address as a SECURE CONTEXT
# (green padlock -> PWA install + offline service worker allowed).
#
# Why a CA and not just one self-signed cert?
#   A bare self-signed cert is NOT trusted -> browser warning -> no secure
#   context -> PWA won't install. So: make a CA once, trust the CA on each
#   device once, then issue/re-issue leaf certs freely without re-importing.
#
# Deps: openssl only. Works on OpenSSL 1.1.1+ / 3.x.
# Run once on any machine (laptop is fine), copy certs/ to the Setu host.
# -----------------------------------------------------------------------------
set -euo pipefail

# ---------- auto-detected (override via env: LAN_IP=… DNS_NAMES="a b" ) ----------
# Detect this host's primary LAN IP — the source IP it uses to reach the network.
detect_ip() {
  local ip=""
  case "$(uname -s)" in
    Darwin)
      # interface for the default route, then its IPv4
      local iface
      iface="$(route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}')"
      [[ -n "$iface" ]] && ip="$(ipconfig getifaddr "$iface" 2>/dev/null || true)"
      # fall back to the usual wired/wifi interfaces
      [[ -z "$ip" ]] && ip="$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || true)"
      ;;
    *)
      # Linux: ask the kernel which src IP reaches a public address (no packet sent)
      ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++)if($i=="src"){print $(i+1);exit}}')"
      [[ -z "$ip" ]] && ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
      ;;
  esac
  printf '%s' "$ip"
}

# Detect this host's name(s) so you can also reach Setu by hostname.
detect_hostnames() {
  local names=()
  case "$(uname -s)" in
    Darwin)
      # Bonjour/mDNS name (what resolves on the LAN as <name>.local)
      local local_name
      local_name="$(scutil --get LocalHostName 2>/dev/null || true)"
      [[ -n "$local_name" ]] && names+=("${local_name}.local" "$local_name")
      ;;
    *)
      local short
      short="$(hostname -s 2>/dev/null || hostname 2>/dev/null || true)"
      [[ -n "$short" ]] && names+=("${short}.local" "$short")
      ;;
  esac
  printf '%s\n' "${names[@]}"
}

LAN_IP="${LAN_IP:-$(detect_ip)}"                   # this host's LAN IP (auto)
if [[ -z "$LAN_IP" ]]; then
  echo "ERROR: could not detect LAN IP. Set it explicitly: LAN_IP=192.168.0.161 $0" >&2
  exit 1
fi

# DNS_NAMES override: space-separated string in the env, else auto-detected.
if [[ -n "${DNS_NAMES:-}" ]]; then
  read -r -a DNS_NAMES <<< "$DNS_NAMES"
else
  DNS_NAMES=("setu.local" "setu")                  # always-reachable aliases
  while IFS= read -r h; do [[ -n "$h" ]] && DNS_NAMES+=("$h"); done < <(detect_hostnames)
fi

CA_DAYS=3650                                       # CA validity (10y)
LEAF_DAYS=825                                      # leaf validity (Apple max=825)
OUT="certs"
echo "-> host IP:    $LAN_IP"
echo "-> hostnames:  ${DNS_NAMES[*]}"
# --------------------------------
# Want RSA instead of EC for crusty old clients? swap both genpkey lines for:
#   openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out <file>

mkdir -p "$OUT"; cd "$OUT"

# 1) Local CA — generated once, reused. ca-key.pem is SECRET, keep it safe.
if [[ ! -f ca-key.pem ]]; then
  openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out ca-key.pem
  openssl req -x509 -new -nodes -key ca-key.pem -sha256 -days "$CA_DAYS" \
    -subj "/CN=Setu Local CA" -out ca.pem
  echo "-> created CA: ca.pem  (import THIS into device trust stores, once)"
fi

# 2) Leaf private key
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out setu-key.pem

# 3) SAN list — modern browsers IGNORE CN, SANs are mandatory.
SAN="IP:${LAN_IP},IP:127.0.0.1,DNS:localhost"
for d in "${DNS_NAMES[@]}"; do SAN="${SAN},DNS:${d}"; done

cat > leaf.cnf <<EOF
[ext]
basicConstraints = CA:FALSE
keyUsage = digitalSignature
extendedKeyUsage = serverAuth
subjectAltName = ${SAN}
EOF

# 4) CSR + sign with the CA
openssl req -new -key setu-key.pem -subj "/CN=setu" -out setu.csr
openssl x509 -req -in setu.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
  -days "$LEAF_DAYS" -sha256 -extfile leaf.cnf -extensions ext -out setu.pem

# 5) fullchain (leaf + CA) — use this if some client lacks the CA in its store
cat setu.pem ca.pem > setu-fullchain.pem

rm -f setu.csr leaf.cnf
echo
echo "Done. Point Setu's config at:"
echo "  TLSCert = $OUT/setu.pem          # or setu-fullchain.pem"
echo "  TLSKey  = $OUT/setu-key.pem"
echo "SANs: ${SAN}"
echo
echo "Re-run anytime to rotate the leaf. Don't delete ca-key.pem (re-import pain)."