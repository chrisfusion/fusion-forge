package fusion.forge.build.entity;

import fusion.forge.build.enums.BuildStatus;
import io.quarkus.hibernate.orm.panache.PanacheEntity;
import io.quarkus.hibernate.orm.panache.PanacheQuery;
import jakarta.persistence.*;

import java.time.Instant;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;

@Entity
@Table(name = "venv_build")
public class VenvBuild extends PanacheEntity {

    @Column(name = "name", nullable = false)
    public String name;

    @Column(name = "version", nullable = false)
    public String version;

    @Column(name = "description", columnDefinition = "TEXT")
    public String description;

    @Enumerated(EnumType.STRING)
    @Column(name = "status", nullable = false)
    public BuildStatus status = BuildStatus.PENDING;

    @Column(name = "creator_id")
    public String creatorId;

    @Column(name = "creator_email")
    public String creatorEmail;

    @Column(name = "index_backend_job_id")
    public Long indexBackendJobId;

    @Column(name = "k8s_job_name")
    public String k8sJobName;

    @Column(name = "k8s_configmap_name")
    public String k8sConfigMapName;

    @Column(name = "created_at", nullable = false)
    public Instant createdAt;

    @Column(name = "updated_at", nullable = false)
    public Instant updatedAt;

    @PrePersist
    void prePersist() {
        createdAt = Instant.now();
        updatedAt = Instant.now();
    }

    @PreUpdate
    void preUpdate() {
        updatedAt = Instant.now();
    }

    public static Optional<VenvBuild> findByNameAndVersion(String name, String version) {
        return find("name = ?1 and version = ?2", name, version).firstResultOptional();
    }

    public static Optional<VenvBuild> findByK8sJobName(String k8sJobName) {
        return find("k8sJobName", k8sJobName).firstResultOptional();
    }

    public static PanacheQuery<VenvBuild> listWithFilters(
            BuildStatus status, String name, String creatorId,
            String sortBy, String sortDir) {
        List<String> conditions = new ArrayList<>();
        Map<String, Object> params = new HashMap<>();

        if (status    != null) { conditions.add("status = :status");         params.put("status",    status); }
        if (name      != null) { conditions.add("lower(name) like :name");   params.put("name",      "%" + name.toLowerCase() + "%"); }
        if (creatorId != null) { conditions.add("creatorId = :creatorId");   params.put("creatorId", creatorId); }

        String predicate = conditions.isEmpty() ? "1=1" : String.join(" and ", conditions);
        return find(predicate + " order by " + sortBy + " " + sortDir, params);
    }
}
