package fusion.forge.client;

import fusion.forge.client.dto.*;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;

import java.util.List;

@RegisterRestClient(configKey = "index-backend")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public interface IndexBackendClient {

    @POST
    @Path("/api/v1/templates")
    IndexTemplateResponse createTemplate(IndexCreateTemplateRequest request);

    @GET
    @Path("/api/v1/templates")
    IndexPageResponse<IndexTemplateResponse> listTemplates(
            @QueryParam("page") @DefaultValue("0") int page,
            @QueryParam("pageSize") @DefaultValue("50") int pageSize);

    @GET
    @Path("/api/v1/templates/{id}/versions")
    List<IndexTemplateVersionResponse> listTemplateVersions(@PathParam("id") long templateId);

    @POST
    @Path("/api/v1/jobs")
    IndexJobResponse createJob(IndexCreateJobRequest request);

    @GET
    @Path("/api/v1/jobs/{id}")
    IndexJobResponse getJob(@PathParam("id") long jobId);
}
