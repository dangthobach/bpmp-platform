// Query/mutation hooks. All cache mutation goes through invalidateQueries so
// the optimistic-lock `version` returned by the server is always re-fetched
// before the next mutation; never patch the cache by hand.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useOrgApi } from "../api/client";
import { orgKeys } from "../api/queryKeys";
import type {
  AddNodeInput,
  CreateOrgInput,
  ListParams,
  MoveNodeInput,
} from "../api/organizationsApi";

export function useOrganizationsList(params: ListParams) {
  const api = useOrgApi();
  return useQuery({
    queryKey: orgKeys.list(params.offset, params.limit),
    queryFn: () => api.list(params),
    staleTime: 30_000,
  });
}

export function useCreateOrganization() {
  const api = useOrgApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateOrgInput) => api.create(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: orgKeys.all }),
  });
}

export function useAddNode() {
  const api = useOrgApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AddNodeInput) => api.addNode(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: orgKeys.all }),
  });
}

export function useMoveNode() {
  const api = useOrgApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: MoveNodeInput) => api.moveNode(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: orgKeys.all }),
  });
}
