// Controlled dialog for creating a root organization. Validates the same
// label rules as the back-end MaterializedPath VO so users get instant
// feedback before round-tripping.
import { useState } from "react";
import { Dialog, Button, Flex, TextField, Text } from "@radix-ui/themes";
import { useCreateOrganization } from "../hooks/useOrganizations";

const CODE_RE = /^[a-z0-9_]{1,64}$/;

interface Props {
  open: boolean;
  onOpenChange: (next: boolean) => void;
}

export function CreateOrganizationDialog({ open, onOpenChange }: Props) {
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const create = useCreateOrganization();

  const codeError =
    code && !CODE_RE.test(code)
      ? "Code must match [a-z0-9_]{1,64}"
      : undefined;
  const valid = !codeError && code.length > 0 && name.length > 0;

  async function onSubmit() {
    if (!valid) return;
    await create.mutateAsync({ code, name });
    setCode("");
    setName("");
    onOpenChange(false);
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Content maxWidth="420px">
        <Dialog.Title>Create organization</Dialog.Title>
        <Dialog.Description size="2" mb="3">
          Root node (kind GROUP). Code becomes the first ltree label.
        </Dialog.Description>
        <Flex direction="column" gap="3">
          <label>
            <Text as="div" size="2" weight="medium" mb="1">Code</Text>
            <TextField.Root
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="acme"
              color={codeError ? "red" : undefined}
            />
            {codeError && (
              <Text size="1" color="red">{codeError}</Text>
            )}
          </label>
          <label>
            <Text as="div" size="2" weight="medium" mb="1">Name</Text>
            <TextField.Root
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Acme Corporation"
            />
          </label>
          {create.isError && (
            <Text size="1" color="red">{(create.error as Error).message}</Text>
          )}
        </Flex>
        <Flex justify="end" gap="2" mt="4">
          <Dialog.Close>
            <Button variant="soft" color="gray">Cancel</Button>
          </Dialog.Close>
          <Button onClick={onSubmit} disabled={!valid || create.isPending}>
            {create.isPending ? "Creating…" : "Create"}
          </Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}
