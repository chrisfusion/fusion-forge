package fusion.forge.build.k8s;

import fusion.forge.build.entity.VenvBuild;
import fusion.forge.build.enums.BuildStatus;
import io.fabric8.kubernetes.api.model.batch.v1.Job;
import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import org.jboss.logging.Logger;

import java.util.List;

/**
 * Reconciles Kubernetes Job status into the VenvBuild DB state every 15 seconds.
 * Also cleans up ConfigMaps for completed or failed builds.
 */
@ApplicationScoped
public class BuildReconciler {

    private static final Logger LOG = Logger.getLogger(BuildReconciler.class);

    @Inject
    KubernetesJobService k8sJobService;

    @Scheduled(every = "15s", delayed = "10s")
    @Transactional
    public void reconcile() {
        List<Job> jobs = k8sJobService.listManagedJobs();
        if (jobs.isEmpty()) return;

        LOG.debugf("Reconciling %d managed K8s jobs", jobs.size());

        for (Job job : jobs) {
            String buildIdStr = job.getMetadata().getAnnotations().get("fusion.forge/build-id");
            if (buildIdStr == null) continue;

            long buildId;
            try {
                buildId = Long.parseLong(buildIdStr);
            } catch (NumberFormatException e) {
                LOG.warnf("Invalid build-id annotation '%s' on job %s",
                        buildIdStr, job.getMetadata().getName());
                continue;
            }

            VenvBuild build = VenvBuild.<VenvBuild>findByIdOptional(buildId).orElse(null);
            if (build == null || build.status == BuildStatus.SUCCESS || build.status == BuildStatus.FAILED) {
                continue;
            }

            BuildStatus derived = deriveStatus(job);
            if (derived != build.status) {
                LOG.infof("Build %d status %s → %s", buildId, build.status, derived);
                build.status = derived;

                if (derived == BuildStatus.SUCCESS || derived == BuildStatus.FAILED) {
                    if (build.k8sConfigMapName != null) {
                        k8sJobService.deleteConfigMap(build.k8sConfigMapName);
                        build.k8sConfigMapName = null;
                    }
                }
            }
        }
    }

    private BuildStatus deriveStatus(Job job) {
        var status = job.getStatus();
        if (status == null) return BuildStatus.BUILDING;

        Integer failed    = status.getFailed();
        Integer succeeded = status.getSucceeded();

        // Check failed first: a job that ends in failure takes priority
        if (failed    != null && failed    > 0) return BuildStatus.FAILED;
        if (succeeded != null && succeeded > 0) return BuildStatus.SUCCESS;
        return BuildStatus.BUILDING;
    }
}
