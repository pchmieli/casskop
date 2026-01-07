date
echo ""
kubectl get pods -n prod-doaks-cassandra | grep cassandra | grep -v backup | cut -d" " -f1 | while read x; do echo $x; kubectl -n prod-doaks-cassandra exec $x -c cassandra --tty -- bash -c 'df -h; du -sh /var/lib/cassandra/*; echo "==="; echo ""'; done