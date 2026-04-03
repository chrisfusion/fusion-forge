package fusion.forge.api;

import fusion.forge.api.auth.CurrentUser;
import fusion.forge.api.dto.CreateVenvRequest;
import fusion.forge.api.dto.ValidationResponse;
import fusion.forge.api.dto.VenvBuildPageResponse;
import fusion.forge.api.dto.VenvBuildResponse;
import fusion.forge.api.mapper.VenvBuildMapper;
import fusion.forge.build.enums.BuildStatus;
import fusion.forge.build.service.VenvBuildService;
import fusion.forge.validation.RequirementsValidator;
import fusion.forge.validation.ValidationResult;
import jakarta.inject.Inject;
import jakarta.validation.Valid;
import jakarta.ws.rs.*;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import org.eclipse.microprofile.openapi.annotations.Operation;
import org.eclipse.microprofile.openapi.annotations.tags.Tag;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;

@Path("/api/v1/venvs")
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
@Tag(name = "Venv Builds")
public class VenvResource {

    private static final int MAX_REQUIREMENTS_BYTES = 100 * 1024; // 100 KB

    @Inject VenvBuildService        buildService;
    @Inject VenvBuildMapper         mapper;
    @Inject CurrentUser             currentUser;
    @Inject RequirementsValidator   validator;

    @GET
    @Operation(summary = "List venv builds with optional filters and pagination")
    public VenvBuildPageResponse list(
            @QueryParam("page")      @DefaultValue("0")         int page,
            @QueryParam("pageSize")  @DefaultValue("20")        int pageSize,
            @QueryParam("status")                               BuildStatus status,
            @QueryParam("name")                                 String name,
            @QueryParam("creatorId")                            String creatorId,
            @QueryParam("sortBy")    @DefaultValue("createdAt") String sortBy,
            @QueryParam("sortDir")   @DefaultValue("desc")      String sortDir) {
        return buildService.list(page, pageSize, status, name, creatorId, sortBy, sortDir);
    }

    @POST
    @Consumes(MediaType.MULTIPART_FORM_DATA)
    @Operation(summary = "Submit a venv package build request")
    public Response create(@Valid CreateVenvRequest req) {
        byte[] requirementsBytes = readRequirements(req);

        ValidationResult result = validator.validate(
                new String(requirementsBytes, StandardCharsets.UTF_8));
        if (!result.valid()) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity(ValidationResponse.from(result))
                    .build();
        }

        var build = buildService.initiate(
                req.name, req.version, req.description,
                currentUser.id, currentUser.email,
                requirementsBytes);

        return Response.accepted(mapper.toResponse(build)).build();
    }

    @POST
    @Path("/validate")
    @Consumes(MediaType.MULTIPART_FORM_DATA)
    @Operation(summary = "Validate a requirements.txt without starting a build")
    public Response validate(@Valid CreateVenvRequest req) {
        byte[] requirementsBytes = readRequirements(req);
        ValidationResponse response = ValidationResponse.from(
                validator.validate(new String(requirementsBytes, StandardCharsets.UTF_8)));
        int statusCode = response.valid() ? 200 : 422;
        return Response.status(statusCode).entity(response).build();
    }

    @GET
    @Path("/{id}")
    @Operation(summary = "Get venv build status and metadata")
    public VenvBuildResponse get(@PathParam("id") Long id) {
        return mapper.toResponse(buildService.findById(id));
    }

    @GET
    @Path("/{id}/logs")
    @Produces(MediaType.TEXT_PLAIN)
    @Operation(summary = "Get build pod logs")
    public Response getLogs(@PathParam("id") Long id) {
        String logs = buildService.getLogs(id);
        if (logs == null) {
            throw new NotFoundException("No pod found for build " + id + " — it may not have started yet");
        }
        if (logs.isEmpty()) {
            return Response.noContent().build();
        }
        return Response.ok(logs).build();
    }

    private byte[] readRequirements(CreateVenvRequest req) {
        byte[] bytes;
        try {
            bytes = Files.readAllBytes(req.requirements.uploadedFile());
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
        if (bytes.length == 0) {
            throw new WebApplicationException("requirements file must not be empty", Response.Status.BAD_REQUEST);
        }
        if (bytes.length > MAX_REQUIREMENTS_BYTES) {
            throw new WebApplicationException(
                    "requirements file exceeds maximum allowed size of 100 KB", Response.Status.BAD_REQUEST);
        }
        return bytes;
    }
}
