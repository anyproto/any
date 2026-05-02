import { useEffect, useReducer } from 'react';
import { Plus, Trash2 } from 'lucide-react';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/Dialog';
import { Button } from '@/components/ui/Button';
import { Input, Textarea } from '@/components/ui/Input';
import { Label } from '@/components/ui/Label';
import { toast } from '@/components/ui/Toast';
import {
  useAddPropertyToType,
  useCreateType,
  type AddPropertyRequest,
} from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';
import {
  canGoNext,
  initial,
  reduce,
  wizardLimits,
  type PropertyDraft,
  type PropertyKindUI,
  type WizardState,
} from './typeWizard';

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const KIND_OPTIONS: { value: PropertyKindUI; label: string }[] = [
  { value: 'string', label: 'Text' },
  { value: 'number', label: 'Number' },
  { value: 'boolean', label: 'Yes / No' },
];

/**
 * Three-step wizard. State machine in typeWizard.ts.
 *
 *   Name → Properties → Confirm → (Creating → Done | Create error)
 */
export function CreateTypeDialog({ open, onOpenChange }: Props) {
  const [state, dispatch] = useReducer(reduce, initial);
  const createType = useCreateType(/* spaceId */ useActiveSpaceId());
  const addProp = useAddPropertyToType(useActiveSpaceId());

  // Reset on close.
  useEffect(() => {
    if (!open) dispatch({ type: 'reset' });
  }, [open]);

  // Drive the create flow when entering 'creating'.
  useEffect(() => {
    if (state.kind !== 'creating') return;
    let cancelled = false;
    void (async () => {
      let typeId: string | null = null;
      let createdProps = 0;
      try {
        const trimmedDesc = state.description.trim();
        const createReq = trimmedDesc
          ? { name: state.name, description: trimmedDesc }
          : { name: state.name };
        const created = await createType.mutateAsync(createReq);
        if (cancelled) return;
        typeId = created.typeId;
        dispatch({ type: 'progress', completed: 1, typeId });

        for (const p of state.properties) {
          if (cancelled) return;
          const req: AddPropertyRequest = { name: p.name.trim(), kind: p.kind };
          await addProp.mutateAsync({ typeId, req });
          createdProps += 1;
          if (cancelled) return;
          dispatch({ type: 'progress', completed: 1 + createdProps });
        }
        dispatch({ type: 'create_ok', typeId });
        toast.success(`Created type “${state.name}”`);
        onOpenChange(false);
      } catch (err) {
        if (cancelled) return;
        const apiErr =
          err instanceof ApiError
            ? err
            : new ApiError({ code: 'unknown', message: String(err) }, 0);
        dispatch({
          type: 'create_failed',
          error: apiErr,
          partial: { typeId, createdProps },
        });
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.kind === 'creating']);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <Step state={state} dispatch={dispatch} />
      </DialogContent>
    </Dialog>
  );
}

function Step({
  state,
  dispatch,
}: {
  state: WizardState;
  dispatch: React.Dispatch<import('./typeWizard').WizardAction>;
}) {
  if (state.kind === 'name') {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Create a type</DialogTitle>
          <DialogDescription>
            A type defines a kind of object — like Recipe, Contact, or Book.
          </DialogDescription>
        </DialogHeader>
        <div className="mt-4 space-y-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="type-name">Name</Label>
            <Input
              id="type-name"
              required
              value={state.name}
              onChange={(e) => dispatch({ type: 'edit_name', name: e.target.value })}
              placeholder="Recipe"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="type-description">Description (optional)</Label>
            <Textarea
              id="type-description"
              value={state.description}
              onChange={(e) =>
                dispatch({ type: 'edit_description', description: e.target.value })
              }
              placeholder="What is this type for?"
              rows={2}
            />
          </div>
        </div>
        <Footer state={state} dispatch={dispatch} />
      </>
    );
  }

  if (state.kind === 'properties') {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Properties of {state.name}</DialogTitle>
          <DialogDescription>
            Add fields you want every {state.name} to have. You can add up to{' '}
            {wizardLimits.MAX_PROPERTIES}.
          </DialogDescription>
        </DialogHeader>
        <ul className="mt-3 flex flex-col gap-2">
          {state.properties.map((p) => (
            <li key={p.rowId}>
              <PropertyRow draft={p} dispatch={dispatch} />
            </li>
          ))}
        </ul>
        <Button
          variant="ghost"
          size="sm"
          className="mt-2 self-start"
          disabled={state.properties.length >= wizardLimits.MAX_PROPERTIES}
          onClick={() => dispatch({ type: 'add_property' })}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden /> Add property
        </Button>
        <Footer state={state} dispatch={dispatch} />
      </>
    );
  }

  if (state.kind === 'confirm') {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Confirm</DialogTitle>
          <DialogDescription>
            Creating this will define the type and add {state.properties.length}{' '}
            {state.properties.length === 1 ? 'property' : 'properties'}.
          </DialogDescription>
        </DialogHeader>
        <dl className="mt-3 grid grid-cols-[7rem_1fr] gap-x-4 gap-y-1 text-sm">
          <dt className="text-foreground/60">Name</dt>
          <dd className="font-medium">{state.name}</dd>
          {state.description.trim() && (
            <>
              <dt className="text-foreground/60">Description</dt>
              <dd className="text-foreground/85">{state.description}</dd>
            </>
          )}
          <dt className="text-foreground/60">Properties</dt>
          <dd>
            {state.properties.length === 0 ? (
              <span className="text-foreground/50">none</span>
            ) : (
              <ul className="space-y-0.5">
                {state.properties.map((p) => (
                  <li key={p.rowId}>
                    <span className="font-medium">{p.name}</span>{' '}
                    <span className="text-foreground/50">— {labelForKind(p.kind)}</span>
                  </li>
                ))}
              </ul>
            )}
          </dd>
        </dl>
        <Footer state={state} dispatch={dispatch} />
      </>
    );
  }

  if (state.kind === 'creating') {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Creating {state.name}…</DialogTitle>
          <DialogDescription>
            {state.progress} of {state.total} steps complete.
          </DialogDescription>
        </DialogHeader>
        <div className="mt-4 h-1.5 w-full overflow-hidden rounded bg-foreground/10">
          <div
            className="h-full rounded bg-accent transition-all"
            style={{ width: `${(state.progress / state.total) * 100}%` }}
            aria-hidden
          />
        </div>
      </>
    );
  }

  if (state.kind === 'create_error') {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Create failed</DialogTitle>
          <DialogDescription>
            <code className="font-mono">{state.error.code}</code> — {state.error.message}
            {state.partial.typeId && (
              <span className="mt-2 block text-foreground/70">
                The type itself was created ({state.partial.createdProps}/
                {state.properties.length} properties added). Retry to finish.
              </span>
            )}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="ghost">Close</Button>
          </DialogClose>
          <Button onClick={() => dispatch({ type: 'start_create' })}>Retry</Button>
        </DialogFooter>
      </>
    );
  }

  // 'done' state shouldn't render — the dialog is closed by the effect.
  return null;
}

function PropertyRow({
  draft,
  dispatch,
}: {
  draft: PropertyDraft;
  dispatch: React.Dispatch<import('./typeWizard').WizardAction>;
}) {
  return (
    <div className="flex items-center gap-2">
      <Input
        aria-label="Property name"
        value={draft.name}
        onChange={(e) =>
          dispatch({ type: 'edit_property_name', rowId: draft.rowId, name: e.target.value })
        }
        placeholder="Name"
        className="flex-1"
      />
      <select
        aria-label="Property kind"
        value={draft.kind}
        onChange={(e) =>
          dispatch({
            type: 'edit_property_kind',
            rowId: draft.rowId,
            kind: e.target.value as PropertyKindUI,
          })
        }
        className={cn(
          'h-8 rounded-md border border-foreground/15 bg-background px-2 text-sm',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        {KIND_OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      <Button
        variant="ghost"
        size="sm"
        aria-label="Remove property"
        onClick={() => dispatch({ type: 'remove_property', rowId: draft.rowId })}
      >
        <Trash2 className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
      </Button>
    </div>
  );
}

function Footer({
  state,
  dispatch,
}: {
  state: WizardState;
  dispatch: React.Dispatch<import('./typeWizard').WizardAction>;
}) {
  return (
    <DialogFooter>
      {state.kind === 'name' && (
        <>
          <DialogClose asChild>
            <Button variant="ghost">Cancel</Button>
          </DialogClose>
          <Button
            disabled={!canGoNext(state)}
            onClick={() => dispatch({ type: 'next_to_properties' })}
          >
            Next
          </Button>
        </>
      )}
      {state.kind === 'properties' && (
        <>
          <Button variant="ghost" onClick={() => dispatch({ type: 'back' })}>
            Back
          </Button>
          <Button
            disabled={!canGoNext(state)}
            onClick={() => dispatch({ type: 'next_to_confirm' })}
          >
            Next
          </Button>
        </>
      )}
      {state.kind === 'confirm' && (
        <>
          <Button variant="ghost" onClick={() => dispatch({ type: 'back' })}>
            Back
          </Button>
          <Button onClick={() => dispatch({ type: 'start_create' })}>Create</Button>
        </>
      )}
    </DialogFooter>
  );
}

function labelForKind(k: PropertyKindUI): string {
  return KIND_OPTIONS.find((o) => o.value === k)?.label ?? k;
}

// Local helper — the dialog only ever renders inside SpaceContents which
// has the active space id, but we read it via the same atom for clarity.
import { useAtomValue } from 'jotai';
import { activeSpaceIdAtom } from '@/atoms/selection';
function useActiveSpaceId(): string | null {
  return useAtomValue(activeSpaceIdAtom);
}
