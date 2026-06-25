#include "OCHXPCRequirement.h"

int OCHSetTeamPeerRequirement(xpc_connection_t connection, const char *signing_identifier) {
    xpc_rich_error_t error = NULL;
    xpc_peer_requirement_t requirement =
        xpc_peer_requirement_create_team_identity(signing_identifier, &error);
    if (requirement == NULL) {
        return -1;
    }

    xpc_connection_set_peer_requirement(connection, requirement);
    xpc_release(requirement);
    return 0;
}

