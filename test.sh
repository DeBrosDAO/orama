for prefix in raft ipfs ipfs-cluster olric; do
  echo -n "$prefix: "
  timeout 3 bash -c "echo | openssl s_client -connect node-hk19de.debros.network:7001 -servername $prefix.node-hk19de.debros.network 2>&1 | grep -q 'CONNECTED' && echo 'OK' || echo 'FAIL'"
done
