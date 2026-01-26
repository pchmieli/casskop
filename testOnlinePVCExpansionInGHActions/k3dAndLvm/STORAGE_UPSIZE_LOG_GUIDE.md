# How to Read Storage-Upsize Test Logs

This guide helps you quickly identify issues in the storage-upsize test logs.

## 🟢 Success Indicators

Look for these in the logs:

### After LVM Installer Deployment
```
✅ Only one loop device attached to disk.img
Number of loop devices attached to disk.img: 1
✅ No duplicate PV UUIDs
```

### Validation Gate
```
✅ VALIDATION PASSED: Only one loop device
Loop devices attached to disk.img: 1
```

### Volume Group Status
```
VG    #PV #LV #SN Attr   VSize  VFree
lvmvg   1   0   0 wz--n- <3.00g <3.00g

PV         VG    Fmt  Attr PSize  PFree
/dev/loop0 lvmvg lvm2 a--  <3.00g <3.00g
```
Note: Only **ONE** PV should be shown, on either `/dev/loop0` or `/dev/loop1` (but not both).

### OpenEBS Can See LVM
```
Verify OpenEBS can see LVM (from openebs-lvm-plugin container):
  VG    #PV #LV #SN Attr   VSize  VFree
  lvmvg   1   0   0 wz--n- <3.00g <3.00g
```

## 🔴 Failure Indicators

### Duplicate Loop Devices
```
❌ ERROR: Multiple loop devices detected for the same disk image!
Number of loop devices attached to disk.img: 2

/dev/loop0: []: (/mnt/disks/disk.img)
/dev/loop1: []: (/mnt/disks/disk.img)
```

### Validation Failure
```
❌ VALIDATION FAILED: Multiple loop devices detected!
This will cause OpenEBS provisioning to fail with duplicate PV error.
❌❌❌ CRITICAL ERROR ❌❌❌
Duplicate loop devices detected!
```
**Action**: The lvm-installer fix didn't work. Check the lvm-installer main container logs to see why multiple loop devices were created.

### Duplicate PV UUIDs
```
WARNING: Not using device /dev/loop1 for PV twwSEI-yjiO-0YaZ-J1Y2-BTu3-BeES-fo6pNU.
WARNING: PV twwSEI-yjiO-0YaZ-J1Y2-BTu3-BeES-fo6pNU prefers device /dev/loop0 because device name matches previous.
❌ DUPLICATE PV UUIDs DETECTED!
```
**Problem**: Same PV UUID exists on multiple loop devices. LVM will refuse to create volumes.

### OpenEBS Provisioning Error
```
E0120 15:42:45.437630 lvm: could not create volume lvmvg/pvc-d0666591-dfd6-47b8-a966-0485b6e15866 cmd [-L 67108864b -n pvc-d0666591-dfd6-47b8-a966-0485b6e15866 lvmvg -y] error:
Cannot update volume group lvmvg with duplicate PV devices.
```
**Problem**: OpenEBS tried to provision a volume but LVM detected duplicate PVs.

### Pod Scheduling Failure
```
Warning   FailedScheduling   pod/cassandra-e2e-dc1-rack1-0   0/1 nodes are available: 1 node(s) did not have enough free storage.
```
Could be:
1. Duplicate PV issue preventing volume creation
2. Actual disk space issue (check node capacity in diagnostics)

## 🔍 Where to Look

### 1. LVM Installer Main Container Logs
Section: `LVM installer main container logs (lvm-setup):`

**Look for**:
```bash
Using loop device: /dev/loop1  # Should be consistent
Physical volume already exists  # OR "Creating LVM physical volume..."
Volume group lvmvg already exists  # OR "Creating LVM volume group: lvmvg"
✅ LVM setup complete
```

**Red flags**:
- Multiple "Setting up LVM volumes..." messages (indicates restart/crash loop)
- Loop device changing between runs
- Error messages

### 2. Validation Section
Section: `VALIDATION: Checking for duplicate loop devices...`

This is the **critical gate**. If it fails here, the test will abort immediately showing:
- How many loop devices are attached
- List of all loop devices
- PV scan showing duplicates
- All PV UUIDs

### 3. OpenEBS LVM Controller Logs
Section: `Check LVM controller logs`

**Look for**:
```
I0120 15:42:45.334792 volume.go:85] Got add event for Vol pvc-d0666591-...
```
Followed by either:
- Success: `Successfully synced 'openebs/pvc-...'` 
- Failure: `ERROR: lvm: could not create volume ... Cannot update volume group lvmvg with duplicate PV devices`

### 4. Background Diagnostics
Section: `BACKGROUND DIAGNOSTICS LOG`

Shows 20-second snapshots of:
- Cassandra pod status
- PVC status (should become Bound)
- Node conditions (DiskPressure should be False)
- Recent events

### 5. Final State
Section: `FINAL STATE`

Check:
- All PVCs should show `Bound` status
- Cassandra pod should be `Running`
- No events with `FailedScheduling` or `ProvisioningFailed`

## 🛠️ Debugging Steps

If the test fails:

1. **Check Validation Section First**: Did we detect duplicate loop devices?
   - Yes → lvm-installer fix failed, check its logs
   - No → Continue investigating

2. **Check LVM Installer Logs**: Were loop devices properly managed?
   ```
   Found existing loop device(s) for disk.img:
   /dev/loop0
   Reusing existing loop device: /dev/loop0
   ```

3. **Check OpenEBS Logs**: Can OpenEBS see the volume group?
   ```
   kubectl exec -n kube-system daemonset/openebs-lvm-node -c openebs-lvm-plugin -- vgs
   ```

4. **Check Node Capacity**: Is there actually disk space?
   ```
   Allocatable:
     ephemeral-storage:  73042796897  # Should be > 1GB
   ```

5. **Check Events**: What does Kubernetes say?
   ```
   kubectl get events --sort-by=.metadata.creationTimestamp | tail -20
   ```

## 📊 Key Metrics

- **Loop devices for disk.img**: Should be exactly **1**
- **PVs in volume group**: Should be exactly **1**
- **VG free space**: Should be close to 3GB
- **Node allocatable storage**: Should be > 1GB
- **PVC status**: Should become `Bound` within 60 seconds
- **Pod status**: Should become `Running` within 120 seconds

## 🎯 Quick Checklist

- [ ] Only 1 loop device attached to disk.img
- [ ] No duplicate PV UUID warnings
- [ ] Validation gate passed
- [ ] OpenEBS can see volume group
- [ ] No "Cannot update volume group with duplicate PV devices" errors
- [ ] PVC becomes Bound
- [ ] Cassandra pod starts Running
- [ ] No FailedScheduling events
