(module
  (type (;0;) (func (param i32 i32 i32 i32) (result i32)))
  (type (;1;) (func (param i32)))
  (type (;2;) (func (param i32 i32)))
  (type (;3;) (func))
  (type (;4;) (func (param i32) (result i32)))
  (type (;5;) (func (param i32 i32) (result i32)))
  (type (;6;) (func (result i32)))
  (type (;7;) (func (param i32 i32 i32 i32)))
  (type (;8;) (func (param i32 i32 i32)))
  (import "wasi_snapshot_preview1" "fd_write" (func (;0;) (type 0)))
  (import "env" "getCaller" (func (;1;) (type 1)))
  (import "env" "hostLogString" (func (;2;) (type 2)))
  (func (;3;) (type 0) (param i32 i32 i32 i32) (result i32)
    (local i32 i32)
    i32.const 0
    local.set 3
    block (result i32)  ;; label = @1
      loop  ;; label = @2
        local.get 2
        local.get 2
        local.get 3
        i32.eq
        br_if 1 (;@1;)
        drop
        local.get 1
        local.get 3
        i32.add
        local.set 4
        local.get 0
        local.get 3
        i32.add
        local.get 3
        i32.const 1
        i32.add
        local.set 3
        i32.load8_u
        local.get 4
        i32.load8_u
        i32.eq
        br_if 0 (;@2;)
      end
      local.get 3
      i32.const 1
      i32.sub
    end
    local.get 2
    i32.ge_u)
  (func (;4;) (type 0) (param i32 i32 i32 i32) (result i32)
    local.get 1
    i32.const -962287725
    i32.mul
    local.get 2
    i32.xor
    i32.const -1130422988
    i32.xor
    local.set 2
    loop  ;; label = @1
      local.get 1
      i32.const 4
      i32.lt_s
      i32.eqz
      if  ;; label = @2
        local.get 0
        i32.load align=1
        local.get 2
        i32.add
        i32.const -962287725
        i32.mul
        local.tee 2
        i32.const 16
        i32.shr_u
        local.get 2
        i32.xor
        local.set 2
        local.get 1
        i32.const 4
        i32.sub
        local.set 1
        local.get 0
        i32.const 4
        i32.add
        local.set 0
        br 1 (;@1;)
      end
    end
    block  ;; label = @1
      block  ;; label = @2
        block  ;; label = @3
          block  ;; label = @4
            local.get 1
            i32.const 1
            i32.sub
            br_table 2 (;@2;) 1 (;@3;) 0 (;@4;) 3 (;@1;)
          end
          local.get 0
          i32.load8_u offset=2
          i32.const 16
          i32.shl
          local.get 2
          i32.add
          local.set 2
        end
        local.get 0
        i32.load8_u offset=1
        i32.const 8
        i32.shl
        local.get 2
        i32.add
        local.set 2
      end
      local.get 2
      local.get 0
      i32.load8_u
      i32.add
      i32.const -962287725
      i32.mul
      local.tee 0
      i32.const 24
      i32.shr_u
      local.get 0
      i32.xor
      local.set 2
    end
    local.get 2)
  (func (;5;) (type 3)
    i32.const 65905
    i32.const 18
    call 6
    unreachable)
  (func (;6;) (type 2) (param i32 i32)
    i32.const 65860
    i32.const 22
    call 8
    local.get 0
    local.get 1
    call 8
    i32.const 10
    call 9
    unreachable)
  (func (;7;) (type 3)
    i32.const 65923
    i32.const 18
    call 6
    unreachable)
  (func (;8;) (type 2) (param i32 i32)
    local.get 1
    i32.const 0
    local.get 1
    i32.const 0
    i32.gt_s
    select
    local.set 1
    loop  ;; label = @1
      local.get 1
      if  ;; label = @2
        local.get 0
        i32.load8_u
        call 9
        local.get 1
        i32.const 1
        i32.sub
        local.set 1
        local.get 0
        i32.const 1
        i32.add
        local.set 0
        br 1 (;@1;)
      end
    end)
  (func (;9;) (type 1) (param i32)
    (local i32 i32)
    i32.const 66012
    i32.load
    local.tee 1
    i32.const 119
    i32.le_u
    if  ;; label = @1
      i32.const 66012
      local.get 1
      i32.const 1
      i32.add
      local.tee 2
      i32.store
      local.get 1
      i32.const 66016
      i32.add
      local.get 0
      i32.store8
      local.get 0
      i32.const 255
      i32.and
      i32.const 10
      i32.ne
      local.get 1
      i32.const 119
      i32.ne
      i32.and
      i32.eqz
      if  ;; label = @2
        i32.const 65964
        local.get 2
        i32.store
        i32.const 1
        i32.const 65960
        i32.const 1
        i32.const 66184
        call 0
        drop
        i32.const 66012
        i32.const 0
        i32.store
      end
      return
    end
    call 5
    unreachable)
  (func (;10;) (type 4) (param i32) (result i32)
    (local i32 i32 i32 i32 i32 i32 i32)
    local.get 0
    i32.eqz
    if  ;; label = @1
      i32.const 66176
      return
    end
    i32.const 66152
    i32.const 66152
    i64.load
    local.get 0
    i64.extend_i32_u
    i64.add
    i64.store
    i32.const 66160
    i32.const 66160
    i64.load
    i64.const 1
    i64.add
    i64.store
    local.get 0
    i32.const 15
    i32.add
    i32.const 4
    i32.shr_u
    local.set 5
    i32.const 66140
    i32.load
    local.tee 4
    local.set 3
    loop  ;; label = @1
      block  ;; label = @2
        block  ;; label = @3
          block  ;; label = @4
            block  ;; label = @5
              local.get 3
              local.get 4
              i32.ne
              if  ;; label = @6
                local.get 2
                local.set 1
                br 1 (;@5;)
              end
              i32.const 1
              local.set 1
              block  ;; label = @6
                block  ;; label = @7
                  local.get 2
                  i32.const 255
                  i32.and
                  br_table 2 (;@5;) 0 (;@7;) 1 (;@6;)
                end
                i32.const 66180
                i32.load
                drop
                global.get 0
                i32.const 65536
                call 11
                i32.const 65536
                i32.const 66480
                call 11
                loop  ;; label = @7
                  i32.const 66177
                  i32.load8_u
                  i32.eqz
                  if  ;; label = @8
                    i32.const 0
                    local.set 2
                    i32.const 0
                    local.set 4
                    i32.const 0
                    local.set 1
                    loop  ;; label = @9
                      block  ;; label = @10
                        block  ;; label = @11
                          i32.const 66144
                          i32.load
                          local.get 1
                          i32.gt_u
                          if  ;; label = @12
                            block  ;; label = @13
                              block  ;; label = @14
                                block  ;; label = @15
                                  block  ;; label = @16
                                    local.get 1
                                    call 12
                                    i32.const 255
                                    i32.and
                                    br_table 3 (;@13;) 0 (;@16;) 1 (;@15;) 2 (;@14;) 6 (;@10;)
                                  end
                                  local.get 1
                                  call 13
                                  i32.const 66168
                                  i32.const 66168
                                  i64.load
                                  i64.const 1
                                  i64.add
                                  i64.store
                                  br 4 (;@11;)
                                end
                                local.get 4
                                i32.const 1
                                i32.and
                                i32.const 0
                                local.set 4
                                i32.eqz
                                br_if 4 (;@10;)
                                local.get 1
                                call 13
                                br 3 (;@11;)
                              end
                              i32.const 0
                              local.set 4
                              i32.const 66136
                              i32.load
                              local.get 1
                              i32.const 2
                              i32.shr_u
                              i32.add
                              local.tee 6
                              local.get 6
                              i32.load8_u
                              i32.const 2
                              local.get 1
                              i32.const 1
                              i32.shl
                              i32.const 6
                              i32.and
                              i32.shl
                              i32.const -1
                              i32.xor
                              i32.and
                              i32.store8
                              br 3 (;@10;)
                            end
                            local.get 2
                            i32.const 16
                            i32.add
                            local.set 2
                            br 2 (;@10;)
                          end
                          i32.const 2
                          local.set 1
                          local.get 2
                          i32.const 66136
                          i32.load
                          i32.const 66480
                          i32.sub
                          i32.const 3
                          i32.div_u
                          i32.ge_u
                          br_if 6 (;@5;)
                          call 14
                          drop
                          br 6 (;@5;)
                        end
                        local.get 2
                        i32.const 16
                        i32.add
                        local.set 2
                        i32.const 1
                        local.set 4
                      end
                      local.get 1
                      i32.const 1
                      i32.add
                      local.set 1
                      br 0 (;@9;)
                    end
                    unreachable
                  end
                  i32.const 0
                  local.set 1
                  i32.const 66177
                  i32.const 0
                  i32.store8
                  i32.const 66144
                  i32.load
                  local.set 2
                  loop  ;; label = @8
                    local.get 1
                    local.get 2
                    i32.ge_u
                    br_if 1 (;@7;)
                    local.get 1
                    call 12
                    i32.const 255
                    i32.and
                    i32.const 3
                    i32.eq
                    if  ;; label = @9
                      local.get 1
                      call 15
                      i32.const 66144
                      i32.load
                      local.set 2
                    end
                    local.get 1
                    i32.const 1
                    i32.add
                    local.set 1
                    br 0 (;@8;)
                  end
                  unreachable
                end
                unreachable
              end
              local.get 2
              local.set 1
              call 14
              i32.const 1
              i32.and
              i32.eqz
              br_if 1 (;@4;)
            end
            i32.const 66144
            i32.load
            local.get 3
            i32.eq
            if  ;; label = @5
              i32.const 0
              local.set 3
              br 2 (;@3;)
            end
            local.get 3
            call 12
            i32.const 255
            i32.and
            if  ;; label = @5
              local.get 3
              i32.const 1
              i32.add
              local.set 3
              br 2 (;@3;)
            end
            local.get 3
            i32.const 1
            i32.add
            local.set 2
            local.get 5
            local.get 7
            i32.const 1
            i32.add
            local.tee 7
            i32.ne
            if  ;; label = @5
              local.get 2
              local.set 3
              br 3 (;@2;)
            end
            i32.const 66140
            local.get 2
            i32.store
            local.get 2
            local.get 5
            i32.sub
            local.tee 2
            i32.const 1
            call 16
            local.get 3
            local.get 5
            i32.sub
            i32.const 2
            i32.add
            local.set 1
            loop  ;; label = @5
              i32.const 66140
              i32.load
              local.get 1
              i32.ne
              if  ;; label = @6
                local.get 1
                i32.const 2
                call 16
                local.get 1
                i32.const 1
                i32.add
                local.set 1
                br 1 (;@5;)
              end
            end
            local.get 2
            i32.const 4
            i32.shl
            i32.const 66480
            i32.add
            local.tee 2
            i32.const 0
            local.get 0
            memory.fill
            local.get 2
            return
          end
          i32.const 65840
          i32.const 13
          call 6
          unreachable
        end
        i32.const 0
        local.set 7
      end
      i32.const 66140
      i32.load
      local.set 4
      local.get 1
      local.set 2
      br 0 (;@1;)
    end
    unreachable)
  (func (;11;) (type 2) (param i32 i32)
    (local i32)
    loop  ;; label = @1
      local.get 0
      local.get 1
      i32.ge_u
      i32.eqz
      if  ;; label = @2
        block  ;; label = @3
          local.get 0
          i32.load
          local.tee 2
          i32.const 66480
          i32.lt_u
          br_if 0 (;@3;)
          local.get 2
          i32.const 66136
          i32.load
          i32.ge_u
          br_if 0 (;@3;)
          local.get 2
          i32.const 66480
          i32.sub
          i32.const 4
          i32.shr_u
          local.tee 2
          call 12
          i32.const 255
          i32.and
          i32.eqz
          br_if 0 (;@3;)
          local.get 2
          call 19
          local.tee 2
          call 12
          i32.const 255
          i32.and
          i32.const 3
          i32.eq
          br_if 0 (;@3;)
          local.get 2
          call 15
        end
        local.get 0
        i32.const 4
        i32.add
        local.set 0
        br 1 (;@1;)
      end
    end)
  (func (;12;) (type 4) (param i32) (result i32)
    i32.const 66136
    i32.load
    local.get 0
    i32.const 2
    i32.shr_u
    i32.add
    i32.load8_u
    local.get 0
    i32.const 1
    i32.shl
    i32.const 6
    i32.and
    i32.shr_u
    i32.const 3
    i32.and)
  (func (;13;) (type 1) (param i32)
    (local i32)
    i32.const 66136
    i32.load
    local.get 0
    i32.const 2
    i32.shr_u
    i32.add
    local.tee 1
    local.get 1
    i32.load8_u
    i32.const 3
    local.get 0
    i32.const 1
    i32.shl
    i32.const 6
    i32.and
    i32.shl
    i32.const -1
    i32.xor
    i32.and
    i32.store8)
  (func (;14;) (type 6) (result i32)
    (local i32 i32 i32)
    memory.size
    memory.grow
    i32.const -1
    i32.ne
    local.tee 1
    if  ;; label = @1
      memory.size
      local.set 0
      i32.const 66008
      i32.load
      local.set 2
      i32.const 66008
      local.get 0
      i32.const 16
      i32.shl
      i32.store
      i32.const 66136
      i32.load
      local.set 0
      call 18
      i32.const 66136
      i32.load
      local.get 0
      local.get 2
      local.get 0
      i32.sub
      memory.copy
    end
    local.get 1)
  (func (;15;) (type 1) (param i32)
    (local i32 i32 i32 i32 i32 i32 i32 i32)
    global.get 0
    i32.const -64
    i32.add
    local.tee 4
    global.set 0
    local.get 4
    i32.const 4
    i32.add
    i32.const 0
    i32.const 60
    memory.fill
    local.get 4
    local.get 0
    i32.store
    local.get 0
    i32.const 3
    call 16
    i32.const 1
    local.set 1
    block  ;; label = @1
      loop  ;; label = @2
        local.get 1
        i32.const 0
        i32.gt_s
        if  ;; label = @3
          local.get 1
          i32.const 1
          i32.sub
          local.tee 1
          i32.const 15
          i32.gt_u
          br_if 2 (;@1;)
          local.get 4
          local.get 1
          i32.const 2
          i32.shl
          i32.add
          i32.load
          local.tee 3
          i32.const 4
          i32.shl
          local.set 0
          block  ;; label = @4
            block  ;; label = @5
              local.get 3
              call 12
              i32.const 255
              i32.and
              i32.const 1
              i32.sub
              br_table 0 (;@5;) 1 (;@4;) 0 (;@5;) 1 (;@4;)
            end
            local.get 3
            i32.const 1
            i32.add
            local.set 3
          end
          local.get 0
          i32.const 66480
          i32.add
          local.set 6
          local.get 3
          i32.const 4
          i32.shl
          local.tee 5
          local.get 0
          i32.sub
          local.set 2
          local.get 5
          i32.const 66480
          i32.add
          local.set 5
          i32.const 66136
          i32.load
          local.set 7
          loop  ;; label = @4
            block  ;; label = @5
              local.get 2
              local.set 0
              local.get 5
              local.get 7
              i32.ge_u
              br_if 0 (;@5;)
              local.get 0
              i32.const 16
              i32.add
              local.set 2
              local.get 5
              i32.const 16
              i32.add
              local.set 5
              local.get 3
              call 12
              local.get 3
              i32.const 1
              i32.add
              local.set 3
              i32.const 255
              i32.and
              i32.const 2
              i32.eq
              br_if 1 (;@4;)
            end
          end
          loop  ;; label = @4
            local.get 0
            i32.eqz
            br_if 2 (;@2;)
            block  ;; label = @5
              local.get 6
              i32.load
              local.tee 2
              i32.const 66480
              i32.lt_u
              br_if 0 (;@5;)
              local.get 2
              i32.const 66136
              i32.load
              i32.ge_u
              br_if 0 (;@5;)
              local.get 2
              i32.const 66480
              i32.sub
              i32.const 4
              i32.shr_u
              local.tee 2
              call 12
              i32.const 255
              i32.and
              i32.eqz
              br_if 0 (;@5;)
              local.get 2
              call 19
              local.tee 2
              call 12
              i32.const 255
              i32.and
              i32.const 3
              i32.eq
              br_if 0 (;@5;)
              local.get 2
              i32.const 3
              call 16
              local.get 1
              i32.const 16
              i32.eq
              if  ;; label = @6
                i32.const 66177
                i32.const 1
                i32.store8
                i32.const 16
                local.set 1
                br 1 (;@5;)
              end
              local.get 1
              i32.const 15
              i32.gt_u
              br_if 4 (;@1;)
              local.get 4
              local.get 1
              i32.const 2
              i32.shl
              i32.add
              local.get 2
              i32.store
              local.get 1
              i32.const 1
              i32.add
              local.set 1
            end
            local.get 0
            i32.const 4
            i32.sub
            local.set 0
            local.get 6
            i32.const 4
            i32.add
            local.set 6
            br 0 (;@4;)
          end
          unreachable
        end
      end
      local.get 4
      i32.const -64
      i32.sub
      global.set 0
      return
    end
    call 5
    unreachable)
  (func (;16;) (type 2) (param i32 i32)
    (local i32)
    i32.const 66136
    i32.load
    local.get 0
    i32.const 2
    i32.shr_u
    i32.add
    local.tee 2
    local.get 2
    i32.load8_u
    local.get 1
    local.get 0
    i32.const 1
    i32.shl
    i32.const 6
    i32.and
    i32.shl
    i32.or
    i32.store8)
  (func (;17;) (type 3)
    i32.const 65882
    i32.const 23
    call 6
    unreachable)
  (func (;18;) (type 3)
    (local i32)
    i32.const 66136
    i32.const 66008
    i32.load
    local.tee 0
    local.get 0
    i32.const 66416
    i32.sub
    i32.const 65
    i32.div_u
    i32.sub
    local.tee 0
    i32.store
    i32.const 66144
    local.get 0
    i32.const 66480
    i32.sub
    i32.const 4
    i32.shr_u
    i32.store)
  (func (;19;) (type 4) (param i32) (result i32)
    (local i32)
    loop  ;; label = @1
      local.get 0
      call 12
      local.get 0
      i32.const 1
      i32.sub
      local.set 0
      i32.const 255
      i32.and
      i32.const 2
      i32.eq
      br_if 0 (;@1;)
    end
    local.get 0
    i32.const 1
    i32.add)
  (func (;20;) (type 4) (param i32) (result i32)
    (local i32 i32 i32)
    global.get 0
    i32.const 32
    i32.sub
    local.tee 1
    global.set 0
    local.get 1
    i32.const 2
    i32.store offset=20
    i32.const 66180
    i32.load
    local.set 3
    i32.const 66180
    local.get 1
    i32.const 16
    i32.add
    i32.store
    local.get 1
    local.get 3
    i32.store offset=16
    block  ;; label = @1
      local.get 0
      if  ;; label = @2
        local.get 0
        i32.const 0
        i32.lt_s
        br_if 1 (;@1;)
        local.get 1
        local.get 0
        call 10
        local.tee 2
        i32.store offset=24
        local.get 1
        local.get 2
        i32.store offset=28
        local.get 1
        local.get 0
        i32.store offset=8
        local.get 1
        local.get 0
        i32.store offset=4
        local.get 1
        local.get 2
        i32.store
        local.get 1
        local.get 2
        i32.store offset=12
        local.get 1
        i32.const 12
        i32.add
        local.get 1
        call 21
      end
      i32.const 66180
      local.get 3
      i32.store
      local.get 1
      i32.const 32
      i32.add
      global.set 0
      local.get 2
      return
    end
    call 7
    unreachable)
  (func (;21;) (type 2) (param i32 i32)
    i32.const 65968
    local.get 0
    local.get 1
    local.get 0
    i32.const 65980
    i32.load
    i32.const 65972
    i32.load
    local.get 0
    call 4
    call 30)
  (func (;22;) (type 1) (param i32)
    (local i32)
    global.get 0
    i32.const 16
    i32.sub
    local.tee 1
    global.set 0
    block  ;; label = @1
      local.get 0
      if  ;; label = @2
        local.get 1
        local.get 0
        i32.store offset=12
        local.get 1
        i32.const 12
        i32.add
        local.get 1
        call 23
        i32.const 1
        i32.and
        i32.eqz
        br_if 1 (;@1;)
        local.get 1
        local.get 0
        i32.store
        local.get 1
        call 24
      end
      local.get 1
      i32.const 16
      i32.add
      global.set 0
      return
    end
    i32.const 65800
    call 25
    unreachable)
  (func (;23;) (type 5) (param i32 i32) (result i32)
    i32.const 65968
    local.get 0
    local.get 1
    local.get 0
    i32.const 65980
    i32.load
    i32.const 65972
    i32.load
    local.get 0
    call 4
    call 29)
  (func (;24;) (type 1) (param i32)
    (local i32 i32 i32 i32 i32 i32 i32 i32 i32)
    global.get 0
    i32.const 32
    i32.sub
    local.tee 1
    global.set 0
    local.get 1
    i32.const 24
    i32.add
    i64.const 0
    i64.store
    local.get 1
    i64.const 0
    i64.store offset=16
    local.get 1
    i32.const 6
    i32.store offset=4
    i32.const 66180
    i32.load
    local.set 6
    i32.const 66180
    local.get 1
    i32.store
    local.get 1
    local.get 6
    i32.store
    local.get 0
    i32.const 65980
    i32.load
    local.tee 3
    i32.const 65972
    i32.load
    i32.const 0
    call 4
    local.set 2
    local.get 1
    i32.const 65968
    i32.load
    local.tee 4
    i32.store offset=8
    local.get 2
    i32.const 24
    i32.shr_u
    local.tee 5
    i32.const 1
    local.get 5
    select
    local.set 7
    local.get 4
    local.get 3
    i32.const 65984
    i32.load
    i32.add
    i32.const 3
    i32.shl
    i32.const 12
    i32.add
    local.get 2
    i32.const -1
    i32.const -1
    i32.const 65988
    i32.load8_u
    local.tee 3
    i32.shl
    i32.const -1
    i32.xor
    local.get 3
    i32.const 31
    i32.gt_u
    select
    i32.and
    i32.mul
    i32.add
    local.set 2
    block  ;; label = @1
      loop  ;; label = @2
        local.get 1
        local.get 2
        i32.store offset=12
        local.get 1
        local.get 2
        i32.store offset=16
        local.get 2
        i32.eqz
        br_if 1 (;@1;)
        i32.const 0
        local.set 3
        block  ;; label = @3
          loop  ;; label = @4
            local.get 3
            i32.const 8
            i32.ne
            if  ;; label = @5
              block  ;; label = @6
                local.get 2
                local.get 3
                i32.add
                local.tee 8
                i32.load8_u
                local.get 7
                i32.ne
                br_if 0 (;@6;)
                i32.const 65980
                i32.load
                local.set 4
                local.get 1
                i32.const 65992
                i32.load
                local.tee 9
                i32.store offset=20
                local.get 1
                i32.const 65996
                i32.load
                local.tee 5
                i32.store offset=24
                local.get 5
                i32.eqz
                br_if 3 (;@3;)
                local.get 0
                local.get 3
                local.get 4
                i32.mul
                local.get 2
                i32.add
                i32.const 12
                i32.add
                local.get 4
                local.get 9
                local.get 5
                call_indirect (type 0)
                i32.const 1
                i32.and
                i32.eqz
                br_if 0 (;@6;)
                local.get 8
                i32.const 0
                i32.store8
                i32.const 65976
                i32.const 65976
                i32.load
                i32.const 1
                i32.sub
                i32.store
                br 5 (;@1;)
              end
              local.get 3
              i32.const 1
              i32.add
              local.set 3
              br 1 (;@4;)
            end
          end
          local.get 1
          local.get 2
          i32.load offset=8
          local.tee 2
          i32.store offset=28
          br 1 (;@2;)
        end
      end
      call 17
      unreachable
    end
    i32.const 66180
    local.get 6
    i32.store
    local.get 1
    i32.const 32
    i32.add
    global.set 0)
  (func (;25;) (type 1) (param i32)
    i32.const 65853
    i32.const 7
    call 8
    local.get 0
    i32.load
    local.get 0
    i32.load offset=4
    call 8
    i32.const 10
    call 9
    unreachable)
  (func (;26;) (type 5) (param i32 i32) (result i32)
    (local i32 i32)
    global.get 0
    i32.const 16
    i32.sub
    local.tee 2
    global.set 0
    i32.const 66180
    i32.load
    local.set 3
    i32.const 66180
    local.get 2
    i32.store
    local.get 0
    local.get 1
    i32.mul
    call 20
    i32.const 66180
    local.get 3
    i32.store
    local.get 2
    i32.const 16
    i32.add
    global.set 0)
  (func (;27;) (type 5) (param i32 i32) (result i32)
    (local i32 i32 i32 i32)
    global.get 0
    i32.const 32
    i32.sub
    local.tee 2
    global.set 0
    local.get 2
    i32.const 2
    i32.store offset=20
    i32.const 66180
    i32.load
    local.set 4
    i32.const 66180
    local.get 2
    i32.const 16
    i32.add
    i32.store
    local.get 2
    local.get 4
    i32.store offset=16
    block  ;; label = @1
      block  ;; label = @2
        block  ;; label = @3
          local.get 1
          i32.eqz
          if  ;; label = @4
            local.get 0
            call 22
            br 1 (;@3;)
          end
          local.get 1
          i32.const 0
          i32.lt_s
          br_if 1 (;@2;)
          local.get 2
          local.get 1
          call 10
          local.tee 3
          i32.store offset=24
          local.get 2
          local.get 3
          i32.store offset=28
          local.get 0
          if  ;; label = @4
            local.get 2
            local.get 0
            i32.store offset=12
            local.get 2
            i32.const 12
            i32.add
            local.get 2
            call 23
            i32.const 1
            i32.and
            i32.eqz
            br_if 3 (;@1;)
            local.get 3
            local.get 2
            i32.load
            local.get 2
            i32.load offset=4
            local.tee 5
            local.get 1
            local.get 1
            local.get 5
            i32.gt_u
            select
            memory.copy
            local.get 2
            local.get 0
            i32.store
            local.get 2
            call 24
          end
          local.get 2
          local.get 1
          i32.store offset=8
          local.get 2
          local.get 1
          i32.store offset=4
          local.get 2
          local.get 3
          i32.store
          local.get 2
          local.get 3
          i32.store offset=12
          local.get 2
          i32.const 12
          i32.add
          local.get 2
          call 21
        end
        i32.const 66180
        local.get 4
        i32.store
        local.get 2
        i32.const 32
        i32.add
        global.set 0
        local.get 3
        return
      end
      call 7
      unreachable
    end
    i32.const 65832
    call 25
    unreachable)
  (func (;28;) (type 3)
    (local i32 i32)
    i32.const 66008
    memory.size
    i32.const 16
    i32.shl
    local.tee 0
    i32.store
    call 18
    i32.const 66136
    i32.load
    local.tee 1
    i32.const 0
    local.get 0
    local.get 1
    i32.sub
    memory.fill
    i32.const 66008
    memory.size
    i32.const 16
    i32.shl
    i32.store)
  (func (;29;) (type 0) (param i32 i32 i32 i32) (result i32)
    (local i32 i32 i32 i32 i32 i32 i32 i32)
    global.get 0
    i32.const 48
    i32.sub
    local.tee 4
    global.set 0
    local.get 4
    i32.const 40
    i32.add
    i32.const 0
    i32.store
    local.get 4
    i64.const 0
    i64.store offset=32
    local.get 4
    i32.const 7
    i32.store offset=12
    i32.const 66180
    i32.load
    local.set 7
    i32.const 66180
    local.get 4
    i32.const 8
    i32.add
    i32.store
    local.get 4
    local.get 7
    i32.store offset=8
    local.get 4
    local.get 0
    i32.load
    local.tee 5
    i32.store offset=16
    local.get 5
    local.get 0
    i32.load offset=16
    local.get 0
    i32.load offset=12
    i32.add
    i32.const 3
    i32.shl
    i32.const 12
    i32.add
    i32.const -1
    i32.const -1
    local.get 0
    i32.load8_u offset=20
    local.tee 6
    i32.shl
    i32.const -1
    i32.xor
    local.get 6
    i32.const 31
    i32.gt_u
    select
    local.get 3
    i32.and
    i32.mul
    i32.add
    local.set 5
    local.get 3
    i32.const 24
    i32.shr_u
    local.tee 3
    i32.const 1
    local.get 3
    select
    local.set 9
    block  ;; label = @1
      block  ;; label = @2
        loop  ;; label = @3
          block  ;; label = @4
            local.get 4
            local.get 5
            i32.store offset=24
            local.get 4
            local.get 5
            i32.store offset=28
            local.get 4
            local.get 5
            i32.store offset=20
            local.get 5
            i32.eqz
            br_if 0 (;@4;)
            i32.const 0
            local.set 3
            loop  ;; label = @5
              local.get 3
              i32.const 8
              i32.ne
              if  ;; label = @6
                block  ;; label = @7
                  local.get 3
                  local.get 5
                  i32.add
                  i32.load8_u
                  local.get 9
                  i32.ne
                  br_if 0 (;@7;)
                  local.get 0
                  i32.load offset=12
                  local.set 6
                  local.get 0
                  i32.load offset=16
                  local.set 10
                  local.get 4
                  local.get 0
                  i32.load offset=24
                  local.tee 11
                  i32.store offset=32
                  local.get 4
                  local.get 0
                  i32.load offset=28
                  local.tee 8
                  i32.store offset=36
                  local.get 8
                  i32.eqz
                  br_if 5 (;@2;)
                  local.get 1
                  local.get 3
                  local.get 6
                  i32.mul
                  local.get 5
                  i32.add
                  i32.const 12
                  i32.add
                  local.get 6
                  local.get 11
                  local.get 8
                  call_indirect (type 0)
                  i32.const 1
                  i32.and
                  i32.eqz
                  br_if 0 (;@7;)
                  local.get 2
                  local.get 3
                  local.get 10
                  i32.mul
                  local.get 6
                  i32.const 3
                  i32.shl
                  i32.add
                  local.get 5
                  i32.add
                  i32.const 12
                  i32.add
                  local.get 0
                  i32.load offset=16
                  memory.copy
                  br 6 (;@1;)
                end
                local.get 3
                i32.const 1
                i32.add
                local.set 3
                br 1 (;@5;)
              end
            end
            local.get 4
            local.get 5
            i32.load offset=8
            local.tee 5
            i32.store offset=40
            br 1 (;@3;)
          end
        end
        local.get 2
        i32.const 0
        local.get 0
        i32.load offset=16
        memory.fill
        br 1 (;@1;)
      end
      call 17
      unreachable
    end
    i32.const 66180
    local.get 7
    i32.store
    local.get 4
    i32.const 48
    i32.add
    global.set 0
    local.get 5
    i32.const 0
    i32.ne)
  (func (;30;) (type 7) (param i32 i32 i32 i32)
    (local i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32)
    global.get 0
    i32.const 256
    i32.sub
    local.tee 4
    global.set 0
    local.get 4
    i32.const 50
    i32.store offset=52
    local.get 4
    i32.const 56
    i32.add
    i32.const 0
    i32.const 200
    memory.fill
    local.get 4
    i32.const 66180
    i32.load
    local.tee 14
    i32.store offset=48
    i32.const 66180
    local.get 4
    i32.const 48
    i32.add
    i32.store
    block  ;; label = @1
      block  ;; label = @2
        local.get 0
        i32.eqz
        br_if 0 (;@2;)
        block  ;; label = @3
          local.get 0
          i32.load8_u offset=20
          local.tee 6
          i32.const 29
          i32.gt_u
          br_if 0 (;@3;)
          local.get 0
          i32.load offset=8
          i32.const 6
          local.get 6
          i32.shl
          i32.le_u
          br_if 0 (;@3;)
          local.get 4
          i64.const 0
          i64.store offset=24
          local.get 4
          local.get 0
          i32.load offset=36
          local.tee 3
          i32.store offset=72
          local.get 4
          local.get 0
          i32.load offset=32
          local.tee 5
          i32.store offset=68
          local.get 4
          local.get 0
          i32.load offset=28
          local.tee 7
          i32.store offset=64
          local.get 4
          local.get 0
          i32.load offset=24
          local.tee 8
          i32.store offset=60
          local.get 4
          local.get 0
          i32.load
          i32.store offset=56
          local.get 4
          local.get 3
          i32.store offset=44
          local.get 4
          local.get 5
          i32.store offset=40
          local.get 4
          local.get 7
          i32.store offset=36
          local.get 4
          local.get 8
          i32.store offset=32
          local.get 4
          local.get 0
          i32.load offset=16
          i32.store offset=24
          local.get 4
          local.get 0
          i32.load offset=12
          i32.store offset=20
          i32.const 65956
          i32.const 65956
          i32.load
          local.tee 3
          i32.const 7
          i32.shl
          local.get 3
          i32.xor
          local.tee 3
          i32.const 1
          i32.shr_u
          local.get 3
          i32.xor
          local.tee 3
          i32.const 9
          i32.shl
          local.get 3
          i32.xor
          local.tee 3
          i32.store
          local.get 4
          i32.const 0
          i32.store offset=16
          local.get 4
          local.get 3
          i32.store offset=12
          local.get 4
          local.get 6
          i32.const 1
          i32.add
          local.tee 3
          i32.store8 offset=28
          local.get 4
          local.get 0
          i32.load offset=16
          local.get 0
          i32.load offset=12
          i32.add
          i32.const 3
          i32.shl
          i32.const 12
          i32.add
          local.get 3
          i32.shl
          call 10
          local.tee 3
          i32.store offset=8
          local.get 4
          local.get 3
          i32.store offset=76
          local.get 4
          local.get 0
          i32.load offset=12
          call 10
          local.tee 8
          i32.store offset=80
          local.get 4
          local.get 0
          i32.load offset=16
          call 10
          local.tee 11
          i32.store offset=84
          i32.const 0
          local.set 7
          i32.const 0
          local.set 3
          i32.const 0
          local.set 5
          i32.const 0
          local.set 6
          loop  ;; label = @4
            local.get 4
            local.get 5
            i32.store offset=88
            local.get 5
            i32.eqz
            if  ;; label = @5
              local.get 4
              local.get 0
              i32.load
              local.tee 5
              i32.store offset=92
              i32.const 1
              local.get 0
              i32.load8_u offset=20
              local.tee 9
              i32.shl
              i32.const 0
              local.get 9
              i32.const 31
              i32.le_u
              select
              local.set 12
            end
            local.get 4
            local.get 5
            i32.store offset=108
            local.get 4
            local.get 5
            i32.store offset=124
            block  ;; label = @5
              loop  ;; label = @6
                local.get 4
                local.get 3
                i32.store offset=96
                local.get 6
                i32.const 255
                i32.and
                i32.const 8
                i32.ge_u
                if  ;; label = @7
                  local.get 3
                  i32.eqz
                  br_if 5 (;@2;)
                  local.get 4
                  local.get 3
                  i32.load offset=8
                  local.tee 3
                  i32.store offset=100
                  i32.const 0
                  local.set 6
                end
                local.get 4
                local.get 3
                i32.store offset=104
                local.get 3
                i32.eqz
                if  ;; label = @7
                  local.get 7
                  local.get 12
                  i32.ge_u
                  br_if 2 (;@5;)
                  local.get 5
                  local.get 0
                  i32.load offset=16
                  local.get 0
                  i32.load offset=12
                  i32.add
                  i32.const 3
                  i32.shl
                  i32.const 12
                  i32.add
                  local.get 7
                  i32.mul
                  i32.add
                  local.set 3
                  local.get 7
                  i32.const 1
                  i32.add
                  local.set 7
                end
                local.get 4
                local.get 3
                i32.store offset=116
                local.get 4
                local.get 3
                i32.store offset=120
                local.get 4
                local.get 3
                i32.store offset=112
                local.get 3
                i32.eqz
                br_if 4 (;@2;)
                local.get 3
                local.get 6
                i32.const 255
                i32.and
                local.tee 10
                i32.add
                i32.load8_u
                i32.eqz
                if  ;; label = @7
                  local.get 6
                  i32.const 1
                  i32.add
                  local.set 6
                  br 1 (;@6;)
                end
                local.get 8
                local.get 0
                i32.load offset=12
                local.tee 9
                local.get 10
                i32.mul
                local.get 3
                i32.add
                i32.const 12
                i32.add
                local.get 9
                memory.copy
                local.get 4
                local.get 0
                i32.load
                local.tee 13
                i32.store offset=128
                block  ;; label = @7
                  local.get 5
                  local.get 13
                  i32.eq
                  if  ;; label = @8
                    local.get 11
                    local.get 10
                    local.get 0
                    i32.load offset=16
                    local.tee 10
                    i32.mul
                    local.get 9
                    i32.const 3
                    i32.shl
                    i32.add
                    local.get 3
                    i32.add
                    i32.const 12
                    i32.add
                    local.get 10
                    memory.copy
                    local.get 6
                    i32.const 1
                    i32.add
                    local.set 6
                    br 1 (;@7;)
                  end
                  local.get 4
                  local.get 0
                  i32.load offset=32
                  local.tee 13
                  i32.store offset=132
                  local.get 4
                  local.get 0
                  i32.load offset=36
                  local.tee 10
                  i32.store offset=136
                  local.get 10
                  i32.eqz
                  br_if 5 (;@2;)
                  local.get 6
                  i32.const 1
                  i32.add
                  local.set 6
                  local.get 0
                  local.get 8
                  local.get 11
                  local.get 8
                  local.get 9
                  local.get 0
                  i32.load offset=4
                  local.get 13
                  local.get 10
                  call_indirect (type 0)
                  call 29
                  i32.const 1
                  i32.and
                  i32.eqz
                  br_if 1 (;@6;)
                end
              end
              local.get 4
              local.get 4
              i32.load offset=40
              local.tee 10
              i32.store offset=140
              local.get 4
              local.get 4
              i32.load offset=44
              local.tee 9
              i32.store offset=144
              local.get 9
              i32.eqz
              br_if 3 (;@2;)
              local.get 4
              i32.const 8
              i32.add
              local.get 8
              local.get 11
              local.get 8
              local.get 4
              i32.load offset=20
              local.get 4
              i32.load offset=12
              local.get 10
              local.get 9
              call_indirect (type 0)
              call 30
              br 1 (;@4;)
            end
          end
          local.get 0
          local.get 4
          i32.load offset=8
          local.tee 3
          i32.store
          local.get 0
          local.get 4
          i64.load offset=12 align=4
          i64.store offset=4 align=4
          local.get 0
          local.get 4
          i64.load offset=20 align=4
          i64.store offset=12 align=4
          local.get 0
          local.get 4
          i32.load8_u offset=28
          i32.store8 offset=20
          local.get 0
          local.get 4
          i32.load offset=32
          local.tee 5
          i32.store offset=24
          local.get 0
          local.get 4
          i32.load offset=36
          local.tee 7
          i32.store offset=28
          local.get 0
          local.get 4
          i32.load offset=40
          local.tee 6
          i32.store offset=32
          local.get 0
          local.get 4
          i32.load offset=44
          local.tee 8
          i32.store offset=36
          local.get 4
          local.get 3
          i32.store offset=148
          local.get 4
          local.get 5
          i32.store offset=152
          local.get 4
          local.get 7
          i32.store offset=156
          local.get 4
          local.get 6
          i32.store offset=160
          local.get 4
          local.get 8
          i32.store offset=164
          local.get 4
          local.get 0
          i32.load offset=32
          local.tee 5
          i32.store offset=168
          local.get 4
          local.get 0
          i32.load offset=36
          local.tee 3
          i32.store offset=172
          local.get 3
          i32.eqz
          br_if 1 (;@2;)
          local.get 1
          local.get 0
          i32.load offset=12
          local.get 0
          i32.load offset=4
          local.get 5
          local.get 3
          call_indirect (type 0)
          local.set 3
          local.get 0
          i32.load8_u offset=20
          local.set 6
        end
        local.get 4
        local.get 0
        i32.load
        local.tee 5
        i32.store offset=176
        local.get 5
        local.get 0
        i32.load offset=16
        local.get 0
        i32.load offset=12
        i32.add
        i32.const 3
        i32.shl
        i32.const 12
        i32.add
        i32.const -1
        i32.const -1
        local.get 6
        i32.const 255
        i32.and
        local.tee 7
        i32.shl
        i32.const -1
        i32.xor
        local.get 7
        i32.const 31
        i32.gt_u
        select
        local.get 3
        i32.and
        i32.mul
        i32.add
        local.set 7
        local.get 3
        i32.const 24
        i32.shr_u
        local.tee 3
        i32.const 1
        local.get 3
        select
        local.set 9
        i32.const 0
        local.set 3
        i32.const 0
        local.set 6
        i32.const 0
        local.set 11
        i32.const 0
        local.set 8
        loop  ;; label = @3
          block  ;; label = @4
            local.get 4
            local.get 7
            local.tee 5
            i32.store offset=212
            local.get 4
            local.get 5
            i32.store offset=216
            local.get 4
            local.get 5
            i32.store offset=196
            local.get 4
            local.get 3
            i32.store offset=192
            local.get 4
            local.get 6
            i32.store offset=188
            local.get 4
            local.get 11
            i32.store offset=184
            local.get 4
            local.get 8
            i32.store offset=180
            local.get 5
            i32.eqz
            br_if 0 (;@4;)
            i32.const 0
            local.set 3
            loop  ;; label = @5
              block  ;; label = @6
                local.get 4
                local.get 11
                i32.store offset=204
                local.get 4
                local.get 6
                i32.store offset=208
                local.get 4
                local.get 8
                i32.store offset=200
                local.get 3
                i32.const 8
                i32.eq
                br_if 0 (;@6;)
                local.get 4
                local.get 8
                local.get 3
                local.get 5
                i32.add
                local.tee 7
                local.get 7
                i32.load8_u
                local.get 6
                i32.or
                local.tee 12
                select
                local.tee 8
                i32.store offset=220
                local.get 4
                local.get 6
                local.get 0
                i32.load offset=12
                local.tee 10
                local.get 3
                i32.mul
                local.get 5
                i32.add
                i32.const 12
                i32.add
                local.tee 13
                local.get 12
                select
                local.tee 6
                i32.store offset=228
                local.get 4
                local.get 11
                local.get 0
                i32.load offset=16
                local.get 3
                i32.mul
                local.get 10
                i32.const 3
                i32.shl
                i32.add
                local.get 5
                i32.add
                i32.const 12
                i32.add
                local.tee 15
                local.get 12
                select
                local.tee 11
                i32.store offset=224
                block  ;; label = @7
                  local.get 7
                  i32.load8_u
                  local.get 9
                  i32.ne
                  br_if 0 (;@7;)
                  local.get 4
                  local.get 0
                  i32.load offset=24
                  local.tee 12
                  i32.store offset=232
                  local.get 4
                  local.get 0
                  i32.load offset=28
                  local.tee 7
                  i32.store offset=236
                  local.get 7
                  i32.eqz
                  br_if 5 (;@2;)
                  local.get 1
                  local.get 13
                  local.get 10
                  local.get 12
                  local.get 7
                  call_indirect (type 0)
                  i32.const 1
                  i32.and
                  i32.eqz
                  br_if 0 (;@7;)
                  local.get 15
                  local.get 2
                  local.get 0
                  i32.load offset=16
                  memory.copy
                  br 6 (;@1;)
                end
                local.get 3
                i32.const 1
                i32.add
                local.set 3
                br 1 (;@5;)
              end
            end
            local.get 4
            local.get 5
            i32.load offset=8
            local.tee 7
            i32.store offset=240
            local.get 5
            local.set 3
            br 1 (;@3;)
          end
        end
        local.get 6
        i32.eqz
        if  ;; label = @3
          local.get 0
          i32.load offset=16
          local.get 0
          i32.load offset=12
          i32.add
          i32.const 3
          i32.shl
          i32.const 12
          i32.add
          call 10
          local.set 5
          local.get 0
          local.get 0
          i32.load offset=8
          i32.const 1
          i32.add
          i32.store offset=8
          local.get 4
          local.get 5
          i32.store offset=248
          local.get 4
          local.get 5
          i32.store offset=252
          local.get 4
          local.get 5
          i32.store offset=244
          local.get 5
          i32.const 12
          i32.add
          local.tee 7
          local.get 1
          local.get 0
          i32.load offset=12
          local.tee 1
          memory.copy
          local.get 7
          local.get 1
          i32.const 3
          i32.shl
          i32.add
          local.get 2
          local.get 0
          i32.load offset=16
          memory.copy
          local.get 5
          local.get 9
          i32.store8
          local.get 3
          i32.eqz
          br_if 1 (;@2;)
          local.get 3
          local.get 5
          i32.store offset=8
          br 2 (;@1;)
        end
        local.get 0
        local.get 0
        i32.load offset=8
        i32.const 1
        i32.add
        i32.store offset=8
        local.get 6
        local.get 1
        local.get 0
        i32.load offset=12
        memory.copy
        local.get 11
        local.get 2
        local.get 0
        i32.load offset=16
        memory.copy
        local.get 8
        i32.eqz
        br_if 0 (;@2;)
        local.get 8
        local.get 9
        i32.store8
        br 1 (;@1;)
      end
      call 17
      unreachable
    end
    i32.const 66180
    local.get 14
    i32.store
    local.get 4
    i32.const 256
    i32.add
    global.set 0)
  (func (;31;) (type 3)
    (local i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i32 i64)
    global.get 0
    i32.const 112
    i32.sub
    local.tee 0
    global.set 0
    local.get 0
    i32.const 18
    i32.store offset=36
    local.get 0
    i32.const 56
    i32.add
    i32.const 0
    i32.const 56
    memory.fill
    local.get 0
    i32.const 66180
    i32.load
    local.tee 8
    i32.store offset=32
    i32.const 66180
    local.get 0
    i32.const 32
    i32.add
    i32.store
    local.get 0
    i32.const 8
    call 10
    local.tee 3
    i32.store offset=48
    local.get 0
    local.get 3
    i32.store offset=52
    local.get 0
    local.get 3
    i32.store offset=44
    local.get 0
    local.get 3
    i32.store offset=40
    local.get 3
    call 1
    local.get 0
    i32.const 24
    i32.add
    local.get 3
    i32.const 42
    call 32
    local.get 0
    i32.load offset=24
    local.get 0
    i32.load offset=28
    call 2
    block  ;; label = @1
      block  ;; label = @2
        local.get 3
        i32.const 99
        i32.le_u
        if  ;; label = @3
          local.get 3
          i32.const 65536
          i32.add
          local.get 3
          i32.const 1
          i32.shl
          i32.const 65572
          i32.add
          local.get 3
          i32.const 10
          i32.lt_u
          local.tee 2
          select
          local.set 1
          i32.const 1
          i32.const 2
          local.get 2
          select
          local.set 3
          br 1 (;@2;)
        end
        i32.const 65
        local.set 2
        local.get 0
        i32.const 65
        call 10
        local.tee 6
        i32.store offset=56
        local.get 3
        local.get 3
        i32.const 31
        i32.shr_s
        local.tee 1
        i32.xor
        local.get 1
        i32.sub
        i64.extend_i32_u
        local.set 12
        local.get 6
        i32.const 2
        i32.sub
        local.set 7
        loop  ;; label = @3
          local.get 12
          i64.const 1000000000
          i64.ge_u
          if  ;; label = @4
            local.get 2
            local.get 7
            i32.add
            local.set 5
            local.get 12
            local.get 12
            i64.const 1000000000
            i64.div_u
            local.tee 12
            i64.const 3294967296
            i64.mul
            i64.add
            i32.wrap_i64
            local.set 4
            i32.const 0
            local.set 1
            loop  ;; label = @5
              local.get 1
              i32.const -8
              i32.ne
              if  ;; label = @6
                local.get 1
                local.get 2
                i32.add
                local.tee 9
                i32.const 1
                i32.sub
                i32.const 64
                i32.gt_u
                br_if 5 (;@1;)
                local.get 1
                local.get 5
                i32.add
                local.tee 10
                i32.const 1
                i32.add
                local.get 4
                local.get 4
                i32.const 100
                i32.div_u
                local.tee 4
                i32.const -100
                i32.mul
                i32.add
                i32.const 1
                i32.shl
                local.tee 11
                i32.const 1
                i32.or
                i32.const 65572
                i32.add
                i32.load8_u
                i32.store8
                local.get 9
                i32.const 2
                i32.sub
                i32.const 64
                i32.gt_u
                br_if 5 (;@1;)
                local.get 10
                local.get 11
                i32.const 65572
                i32.add
                i32.load8_u
                i32.store8
                local.get 1
                i32.const 2
                i32.sub
                local.set 1
                br 1 (;@5;)
              end
            end
            local.get 4
            i32.const 1
            i32.shl
            i32.const 1
            i32.or
            local.tee 4
            i32.const 199
            i32.gt_u
            br_if 3 (;@1;)
            local.get 1
            local.get 2
            i32.add
            i32.const 1
            i32.sub
            local.tee 2
            i32.const 64
            i32.gt_u
            br_if 3 (;@1;)
            local.get 1
            local.get 5
            i32.add
            i32.const 1
            i32.add
            local.get 4
            i32.const 65572
            i32.add
            i32.load8_u
            i32.store8
            br 1 (;@3;)
          end
        end
        local.get 12
        i32.wrap_i64
        local.set 1
        loop  ;; label = @3
          local.get 1
          i32.const 100
          i32.ge_u
          if  ;; label = @4
            local.get 2
            i32.const 1
            i32.sub
            i32.const 64
            i32.gt_u
            br_if 3 (;@1;)
            local.get 2
            local.get 6
            i32.add
            local.tee 4
            i32.const 1
            i32.sub
            local.get 1
            local.get 1
            i32.const 100
            i32.div_u
            local.tee 1
            i32.const -100
            i32.mul
            i32.add
            i32.const 1
            i32.shl
            local.tee 5
            i32.const 1
            i32.or
            i32.const 65572
            i32.add
            i32.load8_u
            i32.store8
            local.get 2
            i32.const 2
            i32.sub
            local.tee 2
            i32.const 64
            i32.gt_u
            br_if 3 (;@1;)
            local.get 4
            i32.const 2
            i32.sub
            local.get 5
            i32.const 65572
            i32.add
            i32.load8_u
            i32.store8
            br 1 (;@3;)
          end
        end
        local.get 2
        i32.const 1
        i32.sub
        local.tee 4
        i32.const 64
        i32.gt_u
        br_if 1 (;@1;)
        local.get 2
        local.get 6
        i32.add
        local.tee 5
        i32.const 1
        i32.sub
        local.get 1
        i32.const 1
        i32.shl
        local.tee 7
        i32.const 1
        i32.or
        i32.const 65572
        i32.add
        i32.load8_u
        i32.store8
        local.get 1
        i32.const 10
        i32.ge_u
        if  ;; label = @3
          local.get 2
          i32.const 2
          i32.sub
          local.tee 4
          i32.const 64
          i32.gt_u
          br_if 2 (;@1;)
          local.get 5
          i32.const 2
          i32.sub
          local.get 7
          i32.const 65572
          i32.add
          i32.load8_u
          i32.store8
        end
        local.get 3
        i32.const 0
        i32.lt_s
        if  ;; label = @3
          local.get 4
          i32.const 1
          i32.sub
          local.tee 4
          i32.const 64
          i32.gt_u
          br_if 2 (;@1;)
          local.get 4
          local.get 6
          i32.add
          i32.const 45
          i32.store8
        end
        local.get 0
        i32.const 65
        local.get 4
        i32.sub
        local.tee 3
        call 10
        local.tee 1
        i32.store offset=68
        local.get 0
        local.get 1
        i32.store offset=72
        local.get 0
        local.get 1
        i32.store offset=64
        local.get 0
        local.get 1
        i32.store offset=60
        local.get 1
        local.get 4
        local.get 6
        i32.add
        local.get 3
        memory.copy
      end
      local.get 0
      local.get 1
      i32.store offset=92
      local.get 0
      local.get 1
      i32.store offset=96
      local.get 0
      local.get 1
      i32.store offset=84
      local.get 0
      local.get 1
      i32.store offset=80
      local.get 0
      local.get 1
      i32.store offset=76
      local.get 3
      i32.const 8
      i32.add
      local.tee 4
      call 10
      local.tee 2
      i64.const 2338042707301724016
      i64.store align=1
      local.get 0
      local.get 2
      i32.store offset=104
      local.get 0
      local.get 2
      i32.store offset=108
      local.get 0
      local.get 2
      i32.store offset=100
      local.get 0
      local.get 2
      i32.store offset=88
      local.get 2
      i32.const 8
      i32.add
      local.get 1
      local.get 3
      memory.copy
      local.get 0
      i32.const 16
      i32.add
      local.get 2
      local.get 4
      call 32
      local.get 0
      i32.load offset=16
      local.get 0
      i32.load offset=20
      call 2
      local.get 0
      i32.const 8
      i32.add
      i32.const 65941
      i32.const 15
      call 32
      local.get 0
      i32.load offset=8
      local.get 0
      i32.load offset=12
      call 2
      i32.const 66180
      local.get 8
      i32.store
      local.get 0
      i32.const 112
      i32.add
      global.set 0
      return
    end
    call 5
    unreachable)
  (func (;32;) (type 8) (param i32 i32 i32)
    (local i32 i32 i32 i32)
    global.get 0
    i32.const 32
    i32.sub
    local.tee 3
    global.set 0
    local.get 3
    i32.const 16
    i32.add
    local.tee 6
    i64.const 0
    i64.store
    local.get 3
    i32.const 24
    i32.add
    local.tee 4
    i32.const 0
    i32.store
    local.get 3
    i64.const 0
    i64.store offset=8
    local.get 3
    i32.const 5
    i32.store offset=4
    i32.const 66180
    i32.load
    local.set 5
    i32.const 66180
    local.get 3
    i32.store
    local.get 3
    local.get 5
    i32.store
    local.get 4
    local.get 2
    call 10
    local.tee 4
    i32.store
    local.get 6
    local.get 4
    i32.store
    local.get 3
    local.get 4
    i32.store offset=20
    local.get 3
    local.get 4
    i32.store offset=12
    local.get 3
    local.get 4
    i32.store offset=8
    local.get 4
    local.get 1
    local.get 2
    memory.copy
    local.get 2
    if  ;; label = @1
      i32.const 66180
      local.get 5
      i32.store
      local.get 0
      local.get 2
      i32.store offset=4
      local.get 0
      local.get 4
      i32.store
      local.get 3
      i32.const 32
      i32.add
      global.set 0
      return
    end
    call 5
    unreachable)
  (table (;0;) 3 3 funcref)
  (memory (;0;) 2)
  (global (;0;) (mut i32) (i32.const 65536))
  (export "memory" (memory 0))
  (export "malloc" (func 20))
  (export "free" (func 22))
  (export "calloc" (func 26))
  (export "realloc" (func 27))
  (export "_start" (func 28))
  (export "run" (func 31))
  (elem (;0;) (i32.const 1) func 3 4)
  (data (;0;) (i32.const 65536) "0123456789abcdefghijklmnopqrstuvwxyz00010203040506070809101112131415161718192021222324252627282930313233343536373839404142434445464748495051525354555657585960616263646566676869707172737475767778798081828384858687888990919293949596979899free: invalid pointer\00\00\00\00\00\00\00\ec\00\01\00\15\00\00\00realloc: invalid pointer\10\01\01\00\18\00\00\00out of memorypanic: panic: runtime error: nil pointer dereferenceindex out of rangeslice out of rangeHello from wasm")
  (data (;1;) (i32.const 65956) "\c1\82\01\00\e0\01\01\00\00\00\00\00\8c\02\01\00\c1\82\01\00\00\00\00\00\04\00\00\00\0c\00\00\00\01\00\00\00\00\00\00\00\01\00\00\00\00\00\00\00\02"))
