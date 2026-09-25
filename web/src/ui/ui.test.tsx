import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '../i18n'
import i18n from '../i18n'
import { Button, ConfirmDialog, Field, Input, SegmentedControl, StatusPill, Textarea } from '.'

describe('Field', () => {
  it('labels the control and keeps help text when an error appears', () => {
    render(
      <Field label="Name" help="Up to 30 characters" error="Required" required>
        <Input />
      </Field>,
    )
    const input = screen.getByLabelText(/Name/)
    const describedBy = input.getAttribute('aria-describedby') ?? ''
    expect(describedBy.split(' ')).toHaveLength(2)
    expect(screen.getByText('Up to 30 characters')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('Required')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAttribute('aria-required', 'true')
  })

  it('counts characters against the limit', () => {
    render(
      <Field label="Prompt">
        <Textarea maxChars={5} value="abcdefg" onChange={() => {}} />
      </Field>,
    )
    expect(screen.getByText('7 / 5')).toBeInTheDocument()
  })
})

describe('Button', () => {
  it('ignores clicks while loading', async () => {
    const onClick = vi.fn()
    render(
      <Button loading onClick={onClick}>
        Save
      </Button>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(onClick).not.toHaveBeenCalled()
    expect(screen.getByRole('button')).toHaveAttribute('aria-busy', 'true')
  })
})

describe('SegmentedControl', () => {
  it('reports the chosen option', async () => {
    const onChange = vi.fn()
    render(
      <SegmentedControl label="Count" value="1" onChange={onChange} options={[{ value: '1', label: '1' }, { value: '4', label: '4' }]} />,
    )
    await userEvent.click(screen.getByRole('radio', { name: '4' }))
    expect(onChange).toHaveBeenCalledWith('4')
  })
})

describe('ConfirmDialog', () => {
  function Harness() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button onClick={() => setOpen(true)}>Delete project</button>
        <ConfirmDialog
          open={open}
          onOpenChange={setOpen}
          title="Delete project?"
          target="Coastal City"
          effects={['3 assets become unassigned']}
          confirmLabel="Delete"
          danger
          onConfirm={() => setOpen(false)}
        />
      </>
    )
  }

  it('names target and effects, closes on Escape and returns focus', async () => {
    await i18n.changeLanguage('en')
    render(<Harness />)
    const trigger = screen.getByRole('button', { name: 'Delete project' })
    await userEvent.click(trigger)
    expect(await screen.findByRole('dialog')).toHaveTextContent('Coastal City')
    expect(screen.getByText('3 assets become unassigned')).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(trigger).toHaveFocus()
  })
})

describe('StatusPill', () => {
  it('labels real statuses and does not invent unknown ones', async () => {
    await i18n.changeLanguage('zh')
    render(
      <>
        <StatusPill status="awaiting_review" />
        <StatusPill status="weird" />
      </>,
    )
    expect(screen.getByText('待确认')).toBeInTheDocument()
    expect(screen.getByText('未知状态')).toBeInTheDocument()
  })
})
