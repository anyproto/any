import { describe, it, expect } from 'vitest';
import { ApiError } from '@/lib/api/client';
import { canGoNext, initial, reduce, type WizardState } from './typeWizard';

const apiErr = new ApiError({ code: 'x', message: 'oops' }, 500);

describe('typeWizard reducer', () => {
  it('starts on the name step with empty fields', () => {
    expect(initial.kind).toBe('name');
  });

  it('edit_name + edit_description update the name step', () => {
    const a = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    const b = reduce(a, { type: 'edit_description', description: 'A dish' });
    expect(b).toEqual({ kind: 'name', name: 'Recipe', description: 'A dish' });
  });

  it('next_to_properties is blocked when name is empty', () => {
    expect(canGoNext(initial)).toBe(false);
    const same = reduce(initial, { type: 'next_to_properties' });
    expect(same).toBe(initial);
  });

  it('next_to_properties advances when name is set', () => {
    const named = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    expect(canGoNext(named)).toBe(true);
    const next = reduce(named, { type: 'next_to_properties' });
    expect(next.kind).toBe('properties');
  });

  it('add / edit / remove property', () => {
    const named = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    const props = reduce(named, { type: 'next_to_properties' });
    const a = reduce(props, { type: 'add_property' });
    expect(a.kind).toBe('properties');
    const aa = a as Extract<WizardState, { kind: 'properties' }>;
    expect(aa.properties.length).toBe(1);

    const renamed = reduce(a, {
      type: 'edit_property_name',
      rowId: aa.properties[0]!.rowId,
      name: 'Servings',
    });
    const r = renamed as Extract<WizardState, { kind: 'properties' }>;
    expect(r.properties[0]!.name).toBe('Servings');

    const kindChanged = reduce(renamed, {
      type: 'edit_property_kind',
      rowId: aa.properties[0]!.rowId,
      kind: 'number',
    });
    const k = kindChanged as Extract<WizardState, { kind: 'properties' }>;
    expect(k.properties[0]!.kind).toBe('number');

    const removed = reduce(kindChanged, {
      type: 'remove_property',
      rowId: aa.properties[0]!.rowId,
    });
    const re = removed as Extract<WizardState, { kind: 'properties' }>;
    expect(re.properties.length).toBe(0);
  });

  it('next_to_confirm blocked when any property name is blank', () => {
    let s: WizardState = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    s = reduce(s, { type: 'next_to_properties' });
    s = reduce(s, { type: 'add_property' });
    expect(canGoNext(s)).toBe(false);
    const blocked = reduce(s, { type: 'next_to_confirm' });
    expect(blocked.kind).toBe('properties');
  });

  it('next_to_confirm advances when all property names are non-blank', () => {
    let s: WizardState = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    s = reduce(s, { type: 'next_to_properties' });
    s = reduce(s, { type: 'add_property' });
    const sp = s as Extract<WizardState, { kind: 'properties' }>;
    s = reduce(s, {
      type: 'edit_property_name',
      rowId: sp.properties[0]!.rowId,
      name: 'Servings',
    });
    s = reduce(s, { type: 'next_to_confirm' });
    expect(s.kind).toBe('confirm');
  });

  it('start_create + progress + create_ok → done', () => {
    let s: WizardState = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    s = reduce(s, { type: 'next_to_properties' });
    s = reduce(s, { type: 'next_to_confirm' });
    s = reduce(s, { type: 'start_create' });
    expect(s.kind).toBe('creating');

    s = reduce(s, { type: 'progress', completed: 1 });
    const c = s as Extract<WizardState, { kind: 'creating' }>;
    expect(c.progress).toBe(1);

    s = reduce(s, { type: 'create_ok', typeId: 't_recipe' });
    expect(s).toEqual({ kind: 'done', typeId: 't_recipe' });
  });

  it('create_failed lands in create_error with partial info', () => {
    let s: WizardState = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    s = reduce(s, { type: 'next_to_properties' });
    s = reduce(s, { type: 'next_to_confirm' });
    s = reduce(s, { type: 'start_create' });
    s = reduce(s, {
      type: 'create_failed',
      error: apiErr,
      partial: { typeId: 't_recipe', createdProps: 0 },
    });
    expect(s.kind).toBe('create_error');
    const e = s as Extract<WizardState, { kind: 'create_error' }>;
    expect(e.partial).toEqual({ typeId: 't_recipe', createdProps: 0 });
  });

  it('back from properties returns to name', () => {
    let s: WizardState = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    s = reduce(s, { type: 'next_to_properties' });
    s = reduce(s, { type: 'back' });
    expect(s).toEqual({ kind: 'name', name: 'Recipe', description: '' });
  });

  it('reset returns to initial', () => {
    let s: WizardState = reduce(initial, { type: 'edit_name', name: 'Recipe' });
    s = reduce(s, { type: 'next_to_properties' });
    s = reduce(s, { type: 'reset' });
    expect(s).toEqual(initial);
  });
});
