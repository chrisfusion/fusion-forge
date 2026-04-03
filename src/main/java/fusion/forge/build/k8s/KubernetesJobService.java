package fusion.forge.build.k8s;

import io.fabric8.kubernetes.api.model.*;
import io.fabric8.kubernetes.api.model.batch.v1.Job;
import io.fabric8.kubernetes.api.model.batch.v1.JobBuilder;
import io.fabric8.kubernetes.client.KubernetesClient;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

import java.util.List;
import java.util.Map;
import java.util.Optional;

@ApplicationScoped
public class KubernetesJobService {

    private static final Logger LOG = Logger.getLogger(KubernetesJobService.class);

    private static final String LABEL_MANAGED_BY_KEY   = "app.kubernetes.io/managed-by";
    private static final String LABEL_MANAGED_BY_VALUE = "fusion-forge";
    private static final String ANNOTATION_BUILD_ID    = "fusion.forge/build-id";

    @Inject
    KubernetesClient k8sClient;

    @ConfigProperty(name = "forge.k8s.namespace")
    String namespace;

    @ConfigProperty(name = "forge.builder.image")
    String builderImage;

    @ConfigProperty(name = "forge.index-backend.url")
    String indexBackendUrl;

    @ConfigProperty(name = "forge.k8s.job.ttl-seconds-after-finished", defaultValue = "86400")
    int ttlSeconds;

    @ConfigProperty(name = "forge.k8s.job.backoff-limit", defaultValue = "0")
    int backoffLimit;

    public record BuildResources(String jobName, String configMapName) {}

    public BuildResources createBuildResources(Long buildId, Long indexBackendJobId,
                                                String venvName, String requirementsTxt) {
        String configMapName = "forge-req-" + buildId;
        String jobName       = "forge-venv-" + buildId;

        createConfigMap(configMapName, buildId, requirementsTxt);
        createJob(jobName, configMapName, buildId, indexBackendJobId, venvName);

        return new BuildResources(jobName, configMapName);
    }

    private void createConfigMap(String name, Long buildId, String requirementsTxt) {
        ConfigMap cm = new ConfigMapBuilder()
                .withNewMetadata()
                    .withName(name)
                    .withNamespace(namespace)
                    .withLabels(Map.of(
                            LABEL_MANAGED_BY_KEY, LABEL_MANAGED_BY_VALUE,
                            "fusion.forge/build-id", String.valueOf(buildId)))
                .endMetadata()
                .withData(Map.of("requirements.txt", requirementsTxt))
                .build();

        k8sClient.configMaps().inNamespace(namespace).resource(cm).create();
        LOG.debugf("ConfigMap %s created", name);
    }

    private void createJob(String jobName, String configMapName, Long buildId,
                            Long indexBackendJobId, String venvName) {
        Job job = new JobBuilder()
                .withNewMetadata()
                    .withName(jobName)
                    .withNamespace(namespace)
                    .withLabels(Map.of(
                            LABEL_MANAGED_BY_KEY,       LABEL_MANAGED_BY_VALUE,
                            "app.kubernetes.io/component", "venv-builder"))
                    .withAnnotations(Map.of(
                            ANNOTATION_BUILD_ID,           String.valueOf(buildId),
                            "fusion.forge/venv-name",      venvName))
                .endMetadata()
                .withNewSpec()
                    .withTtlSecondsAfterFinished(ttlSeconds)
                    .withBackoffLimit(backoffLimit)
                    .withNewTemplate()
                        .withNewMetadata()
                            .withLabels(Map.of(
                                    LABEL_MANAGED_BY_KEY,    LABEL_MANAGED_BY_VALUE,
                                    "fusion.forge/build-id", String.valueOf(buildId)))
                        .endMetadata()
                        .withNewSpec()
                            .withRestartPolicy("Never")
                            .withServiceAccountName("fusion-forge-builder")
                            .withContainers(List.of(
                                new ContainerBuilder()
                                    .withName("builder")
                                    .withImage(builderImage)
                                    .withEnv(List.of(
                                        new EnvVarBuilder()
                                            .withName("INDEX_BACKEND_URL")
                                            .withValue(indexBackendUrl)
                                            .build(),
                                        new EnvVarBuilder()
                                            .withName("JOB_ID")
                                            .withValue(String.valueOf(indexBackendJobId))
                                            .build(),
                                        new EnvVarBuilder()
                                            .withName("VERSION_NUMBER")
                                            .withValue("1")  // index-backend always starts at version 1
                                            .build(),
                                        new EnvVarBuilder()
                                            .withName("VENV_NAME")
                                            .withValue(venvName)
                                            .build()
                                    ))
                                    .withVolumeMounts(List.of(
                                        new VolumeMountBuilder()
                                            .withName("requirements")
                                            .withMountPath("/workspace/requirements.txt")
                                            .withSubPath("requirements.txt")
                                            .build()
                                    ))
                                    .withNewResources()
                                        .withRequests(Map.of(
                                            "cpu",    new Quantity("500m"),
                                            "memory", new Quantity("512Mi")))
                                        .withLimits(Map.of(
                                            "cpu",    new Quantity("2000m"),
                                            "memory", new Quantity("2Gi")))
                                    .endResources()
                                    .build()
                            ))
                            .withVolumes(List.of(
                                new VolumeBuilder()
                                    .withName("requirements")
                                    .withNewConfigMap()
                                        .withName(configMapName)
                                    .endConfigMap()
                                    .build()
                            ))
                        .endSpec()
                    .endTemplate()
                .endSpec()
                .build();

        k8sClient.batch().v1().jobs().inNamespace(namespace).resource(job).create();
        LOG.infof("K8s Job %s created for build %d", jobName, buildId);
    }

    public Optional<Job> findJob(String jobName) {
        return Optional.ofNullable(
                k8sClient.batch().v1().jobs().inNamespace(namespace).withName(jobName).get());
    }

    public List<Job> listManagedJobs() {
        return k8sClient.batch().v1().jobs().inNamespace(namespace)
                .withLabel(LABEL_MANAGED_BY_KEY, LABEL_MANAGED_BY_VALUE)
                .list()
                .getItems();
    }

    public String getPodLogs(Long buildId) {
        var pods = k8sClient.pods().inNamespace(namespace)
                .withLabel("fusion.forge/build-id", String.valueOf(buildId))
                .list()
                .getItems();

        if (pods.isEmpty()) return null;

        var pod = pods.get(0);
        String phase = pod.getStatus() != null ? pod.getStatus().getPhase() : null;
        if ("Pending".equals(phase)) return "";

        return k8sClient.pods().inNamespace(namespace)
                .withName(pod.getMetadata().getName())
                .getLog();
    }

    public void deleteConfigMap(String configMapName) {
        try {
            k8sClient.configMaps().inNamespace(namespace).withName(configMapName).delete();
            LOG.debugf("ConfigMap %s deleted", configMapName);
        } catch (Exception e) {
            LOG.warnf("Could not delete ConfigMap %s: %s", configMapName, e.getMessage());
        }
    }
}
