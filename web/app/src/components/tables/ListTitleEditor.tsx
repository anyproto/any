import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Input } from '@/components/ui';
import { ListIcon, ListIconDialog } from '@/components/types';
import { cn } from '@/lib/cn';

export function ListTitleEditor({
  name,
  icon,
  onRename,
  onIconChange,
}: {
  name: string;
  icon?: string | undefined;
  onRename: (name: string) => void;
  onIconChange: (icon: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(name);
  const [iconOpen, setIconOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!editing) setDraft(name);
  }, [editing, name]);

  useEffect(() => {
    if (!editing) return;
    const id = window.setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, 0);
    return () => window.clearTimeout(id);
  }, [editing]);

  const commit = () => {
    const next = draft.trim();
    if (next && next !== name) onRename(next);
    setEditing(false);
  };

  const cancel = () => {
    setDraft(name);
    setEditing(false);
  };

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    commit();
  };

  return (
    <div className="flex min-w-0 items-center gap-2">
      <button
        type="button"
        aria-label={icon ? `Change ${name} list icon` : `Add icon to ${name} list`}
        onClick={() => setIconOpen(true)}
        className={cn(
          'inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px]',
          'text-foreground/55 hover:bg-foreground/[0.055] hover:text-foreground',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <ListIcon icon={icon} className="h-5 w-5 text-[20px]" />
      </button>
      {editing ? (
        <form onSubmit={submit} className="min-w-0 flex-1">
          <h1 className="min-w-0">
            <Input
              ref={inputRef}
              aria-label="List name"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={commit}
              onKeyDown={(e) => {
                if (e.key === 'Escape') {
                  e.preventDefault();
                  cancel();
                }
              }}
              className="h-11 max-w-xl border-0 bg-foreground/[0.055] px-2 text-[34px] font-bold leading-none shadow-none focus-visible:ring-1"
            />
          </h1>
        </form>
      ) : (
        <h1 className="min-w-0 truncate text-[34px] font-bold leading-[1.08] text-foreground">
          <button
            type="button"
            title={`Rename ${name} list`}
            onClick={() => setEditing(true)}
            className={cn(
              'min-w-0 max-w-full truncate rounded-[10px] px-1 text-left',
              'hover:bg-foreground/[0.045]',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            {name}
          </button>
        </h1>
      )}
      <ListIconDialog
        open={iconOpen}
        icon={icon}
        onOpenChange={setIconOpen}
        onSelect={onIconChange}
      />
    </div>
  );
}
