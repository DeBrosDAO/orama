package inspector

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// ipfsClusterSecretPath is the cluster secret on an installed node. The REST
// API password is derived from it (ipfs.ClusterRESTPassword); service.json is
// the orama user's file and is not a place to read a curl config from.
const ipfsClusterSecretPath = "/opt/orama/.orama/secrets/cluster-secret"

// clusterRESTPasswordProgram prints the REST API password for the secret file
// named by argv[1]. It is HKDF-SHA256 with a 32-byte zero salt and the info
// string ipfs.ClusterRESTPassword uses, which is what golang.org/x/crypto/hkdf
// does for a nil salt. The file is opened with O_NOFOLLOW: the orama user owns
// the directory it sits in. The program contains no single quotes; it is
// embedded in a single-quoted python3 -c argument.
const clusterRESTPasswordProgram = `import os,sys,hmac,hashlib,binascii
p=sys.argv[1]
fd=os.open(p, os.O_RDONLY|os.O_NOFOLLOW)
try:
    data=os.read(fd, 4096)
    extra=os.read(fd, 1)
finally:
    os.close(fd)
ikm=data.strip()
if extra or not ikm:
    raise SystemExit(1)
prk=hmac.new(b"\x00"*32, ikm, hashlib.sha256).digest()
okm=hmac.new(prk, b"ipfs-cluster-rest-api"+bytes([1]), hashlib.sha256).digest()
sys.stdout.write(binascii.hexlify(okm).decode())`

// ipfsClusterCurl is a shell pipeline that GETs path from the node's IPFS
// Cluster REST API, which requires basic auth.
//
// The password is derived on the node, as root, from the cluster secret — the
// same derivation install wrote into service.json. The orama user can write
// service.json, and the previous pipeline parsed that file into `curl -K -`
// running as the operator, so a value with a newline became another curl
// directive. The derived password is 64 hex characters; the shell checks that
// before anything reaches curl, and curl reads the config on stdin so the
// password is not on a command line. The whole pipeline is a subshell so a
// refusal does not exit the inspector script around it.
// kuboAPITokenProgram prints the Kubo RPC bearer. It is the same HKDF as
// ipfs.KuboAPIToken, info "ipfs-kubo-api". No single quotes: the program is
// embedded in a single-quoted python3 -c argument.
const kuboAPITokenProgram = `import os,sys,hmac,hashlib,binascii
p=sys.argv[1]
fd=os.open(p, os.O_RDONLY|os.O_NOFOLLOW)
try:
    data=os.read(fd, 4096)
    extra=os.read(fd, 1)
finally:
    os.close(fd)
ikm=data.strip()
if extra or not ikm:
    raise SystemExit(1)
prk=hmac.new(b"\x00"*32, ikm, hashlib.sha256).digest()
okm=hmac.new(prk, b"ipfs-kubo-api"+bytes([1]), hashlib.sha256).digest()
sys.stdout.write(binascii.hexlify(okm).decode())`

// ipfsKuboCurl POSTs path (including the query) to this node's Kubo RPC. The
// bearer is derived on the node and handed to curl on stdin, not on the
// command line. The pipeline is a subshell so a refusal does not exit the
// inspector script around it.
func ipfsKuboCurl(path string) string {
	url := fmt.Sprintf("http://localhost:%d%s", constants.IPFSAPIPort, path)
	return fmt.Sprintf(
		`(tok=$(%spython3 -c '%s' %q) || exit 1; printf '%%s\n' "$tok" | grep -Eq '^[0-9a-f]{64}$' || exit 1; printf 'header = "Authorization: Bearer %%s"\n' "$tok" | curl -sf -K - -X POST %q)`,
		inspectorSudo, kuboAPITokenProgram, ipfsClusterSecretPath, url)
}

func ipfsClusterCurl(curlOpts, path string) string {
	url := fmt.Sprintf("http://localhost:%d%s", constants.IPFSClusterAPIPort, path)
	return fmt.Sprintf(
		`(pw=$(%spython3 -c '%s' %q) || exit 1; printf '%%s\n' "$pw" | grep -Eq '^[0-9a-f]{64}$' || exit 1; printf 'user = "orama:%%s"\n' "$pw" | curl -sf %s -K - %q)`,
		inspectorSudo, clusterRESTPasswordProgram, ipfsClusterSecretPath, curlOpts, url)
}
