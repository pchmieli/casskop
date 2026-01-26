  helm repo add longhorn https://charts.longhorn.io
    helm repo update
    kubectl create namespace longhorn-system
    helm install longhorn longhorn/longhorn --namespace longhorn-system
    
    # 2) Make Longhorn default (optional)
    kubectl patch storageclass local-path -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}'
    kubectl patch storageclass longhorn -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
    
    # 3) Create PVC
    cat <<'YAML' | kubectl apply -f -
  apiVersion: v1
  kind: PersistentVolumeClaim
  metadata:
    name: data-pvc
  spec:
    accessModes:
      - ReadWriteOnce
    storageClassName: longhorn
    resources:
      requests:
        storage: 5Gi
    YAML
    
    # 4) Create a Pod using it
    cat <<'YAML' | kubectl apply -f -
  apiVersion: v1
  kind: Pod
  metadata:
    name: pvc-tester
  spec:
    containers:
      - name: app
        image: busybox
        command: ["sh", "-c", "sleep 360000"]
        volumeMounts:
          - name: data
            mountPath: /data
    volumes:
      - name: data
        persistentVolumeClaim:
          claimName: data-pvc
    YAML
    
    # 5) Resize PVC to 10Gi
    kubectl patch pvc data-pvc -p '{"spec":{"resources":{"requests":{"storage":"10Gi"}}}}'
    kubectl describe pvc data-pvc