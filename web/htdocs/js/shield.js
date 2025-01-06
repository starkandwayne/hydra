// vim:et:sts=2:ts=2:sw=2
var referer;

function divert(page) { // {{{
  if (page.match(/^#!\/(login|logout|cliauth)$/)) {
    /* never divert these pages */
    return page;
  }

  if (!AEGIS.authenticated()) {
    console.log('session not authenticated; diverting to #!/login page...');
    return "#!/login";
  }

  if (AEGIS.is('engineer') && AEGIS.shield) {
    /* process 'system' team diverts */
    if (AEGIS.vault == "uninitialized") {
      console.log('system user detected, and this SHIELD core is uninitialized; diverting to #!/init page...');
      return "#!/init";
    }
  }

  if (!page || page == "") {
    return AEGIS.is('engineer') ? '#!/admin' : '#!/systems';
  }

  return page;
}
// }}}

function dispatch(page) {
  var argv = page.split(/[:+]/);
  dest = argv.shift();
  page = divert(dest);
  args = {};
  for (var i = 0; i < argv.length; i += 2) {
    args[argv[0+i]] = argv[1+i]
  }

  console.log('dispatching to %s (from "%s")...', page, dest);

  var top = page.replace(/^(#!\/[^\/]+)(\/.*)?$/, '$1');
  $('nav li.current').removeClass('current');
  $('nav a[href="'+top+'"]').closest('li').addClass('current');

  Scratch.clear();
  Scratch.track('redrawable', true);
  switch (page) {

  case "#!/login": /* {{{ */
    (function () {
      var progress = function (how) {
        $('#viewport').find('#logging-in').remove();
        $('#viewport').append($.template('logging-in', {auth: how}));
      };

      $.when(
        $.ajax({ type: 'GET', url: '/v2/auth/providers?for=web' }),
        $.ajax({ type: 'GET', url: '/v2/info' })
      ).then(function (providers, info) {
        $('#viewport').html($($.template('login', { providers: providers[0],
                                                    info:      info[0] }))
          .on("click", ".login", function (event) {
            progress($(event.target).text());
          })
          .on("submit", "form", function (event) {
            event.preventDefault()
            progress('local SHIELD authentication');

            var $form = $(event.target);
            var data = $form.serializeObject();
            $form.reset();

            api({
              type: "POST",
              url:  "/v2/auth/login",
              data: data,
              success: function () {
                /* this makes the chrome re-render unnecessary */
                document.location.href = "/"
              },
              error: function (xhr) {
                $(event.target).error(xhr.responseJSON);
              },
              complete: function () {
                $('#viewport').find('#logging-in').remove();
                //using the systems page as our landing page when a user logs in
                document.location.href = "/#!/systems"
              }
            });
          }));
      }, function () {
        console.dir(arguments);
        $('#viewport').template('BOOM');
      });
    })();
    break; /* #!/login */
    // }}}
  case "#!/cliauth": /* {{{ */
    $('#viewport').template('cliauth', args);
    break; /* #!/cliauth */
    // }}}
  case "#!/logout": /* {{{ */
    (function () {
      api({
        type: "GET",
        url: "/v2/auth/logout",
        success: function () {
          document.location.href = '/';
        },
        error: function (xhr) {
          if (xhr.status >= 500) {
            $('#viewport').template('BOOM');
          } else {
            document.location.href = '/';
          }
        }
      });
    })()
    break;
    // }}}
    case "#!/init": /* {{{ */
      (function () {
      $('#viewport').template('decide');
      $('#viewport').html($($.template('decide'))
        .on("submit", ".restore", function (event) {
          $('#viewport').template('restore');
          $('#viewport').html($($.template('restore'))
            .on("submit", ".restore", function (event) {
              event.preventDefault();

              var $form = $(event.target);
              var data = new FormData();

              data.append("archive", $form[0].archive.files[0]);
              data.append("key",     $form[0].key.value);
              data.append("token",   $form[0].token.value);
              data.append("masterpass",   $form[0].masterpass.value);
              data.append("masterpassconf",   $form[0].masterpassconf.value);

              if (data.masterpass == "") {
                $form.error('masterpass', 'missing');

              } else if (data.masterpassconf == "") {
                $form.error('masterpassconf', 'missing');

              } else if (data.masterpass != data.masterpassconf) {
                $form.error('masterpassconf', 'mismatch');
              }

              if (!$form.isOK()) {
                return;
              }

              $form.reset();
              $('.dialog').template('bootstrap', { step: 'restoring' });

              $.ajax({
                type: "POST",
                url: "/v2/bootstrap/restore",
                data: data,
                cache: false,
                contentType: false,
                processData: false,
                success: function (data) {
                  $('#viewport').template('fixedkey', data);
                },
                error: function (xhr) {
                  $form.submitting(false);
                  $(event.target).error(xhr.responseJSON);
                }
              });
            })
          );
        })
        .on("submit", ".setpass", function (event) {
          $('#viewport').template('init');
          $('#viewport').html($($.template('init'))
            .on("submit", ".setpass", function (event) {
              event.preventDefault();
              var $form = $(event.target);
              var data = $form.serializeObject();
              if (data.masterpass == "") {
                $form.error('masterpass', 'missing');

              } else if (data.masterpassconf == "") {
                $form.error('masterpassconf', 'missing');

              } else if (data.masterpass != data.masterpassconf) {
                $form.error('masterpassconf', 'mismatch');
              }

              if (!$form.isOK()) {
                return;
              }
              $form.submitting(true);
              api({
                type: 'POST',
                url: '/v2/init',
                data: { "master": data.masterpass },
                success: function (data) {
                  console.log("success");
                  $('#viewport').template('fixedkey', data);
                },
                error: function (xhr) {
                  $form.submitting(false);
                  $(event.target).error(xhr.responseJSON);
                }
              });
            })
          );
        })
      );
        $.ajax({
          type: "GET",
          url: "/v2/bootstrap/log",
          success: function (data) {
            if (data.log) {
              $('.restore_divert').html("It looks like there was a previous attempt to self-restore SHIELD that failed. Below is the task log to help debug the problem. ")
              $('#initialize').append("<div class=\"dialog\" id=\"log\"></div>")
              $('#log').append('<pre><code>'+h(data.log)+'</code></pre>');
            }
          }
        });
      })();
      break; /* #!/init */
    // }}}

  case "#!/do/backup": /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    if (!AEGIS.is('tenant', 'operator')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'operator' });
      break;
    }
    $('#main').template('do-backup');
    $('#main .do-backup').trigger('wizard:step', { to: 1 });
    break; /* #!/do/backup */
    // }}}
  case "#!/do/restore": /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    if (!AEGIS.is('tenant', 'operator')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'operator' });
      break;
    }

    $('#main').template('do-restore');
    $('#main .do-restore').trigger('wizard:step', { to: 1 });
    break; /* #!/do/restore */
    // }}}
  case "#!/do/configure": /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    if (!AEGIS.is('tenant', 'engineer')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'engineer' });
      break;
    }

    var data = {};
    $('#main').template('do-configure', data);
    $(document.body)
      .on('change', '#main select[name="target.plugin"]', function (event) {
        data.selected_target_plugin = $(event.target).val();
        $('#main .redraw.target').template('do-configure-target-plugin', data)
                                .find('[name="target.agent"]').focus();
      })
      .on('change', '#main select[name="target.agent"]', function (event) {
        data.selected_target_agent = $(event.target).val();
        $('#main .redraw.target').template('do-configure-target-plugin', data)
                                 .find('.plugin0th input').focus();
      })
      .on('change', '#main select[name="store.plugin"]', function (event) {
        data.selected_store_plugin = $(event.target).val();
        $('#main .redraw.store').template('do-configure-store-plugin', data)
                                .find('[name="store.agent"]').focus();
      })
      .on('change', '#main select[name="store.agent"]', function (event) {
        data.selected_store_agent = $(event.target).val();
        $('#main .redraw.store').template('do-configure-store-plugin', data)
                                .find('.plugin0th input').focus();
      });
    window.setTimeout(function () {
      $('#main .do-configure').trigger('wizard:step', { to: 1 });
      $('#main .optgroup').optgroup();
      $('#main .scheduling [data-subform=schedule-daily]').trigger('click');
    }, 150);
    break; /* #!/do/configure */
    // }}}

  case "#!/systems": /* {{{ */
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    $('#main').template('systems');
    break; /* #!/systems */
    // }}}
  case '#!/systems/system': /* {{{ */
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    $('#main').template('system', args);
    $('#main .paginate .load-more').trigger('click');
    api({
      type:     'GET',
      url:      '/v2/tenants/'+AEGIS.current.uuid+'/systems/'+args.uuid+'/config'
    }, true).then(function (config) {
      args.config = config;
      $('#main').template('system', args);
    });
    break; /* #!/systems/system */
    // }}}
  case '#!/systems/edit': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('tenant', 'engineer')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'engineer' });
      break;
    }
    $('#main').html($($.template('targets-form', args))
      .autofocus()
      .on('submit', 'form', function (event) {
        event.preventDefault();

        var $form = $(event.target);
        if (!$form.reset().validate().isOK()) { return; }

        var data = $form.serializeObject();
        $form.submitting(true);
        api({
          type: 'PUT',
          url:  '/v2/tenants/'+AEGIS.current.uuid+'/targets/'+args.uuid,
          data: data,
          success: function () {
            goto("#!/systems/system:uuid:"+args.uuid);
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
      }));

    break; /* #!/systems/edit */
    // }}}

  case '#!/stores': /* {{{ */
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    $('#main').template('stores');
    break; /* #!/stores */
    // }}}
  case '#!/stores/store': /* {{{ */
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }

    var store = AEGIS.store(args.uuid);
    $('#main').template('store', args);
    api({
      type:     'GET',
      url:      (store && store.global
                   ? '/v2/global/stores/'+args.uuid+'/config'
                   : '/v2/tenants/'+AEGIS.current.uuid+'/stores/'+args.uuid+'/config'),
      dataType: 'json',
    }, true).then(function (config) {
      args.config = config || [];
      $('#main').template('store', args)
        .find('.paginate .load-more').trigger('click');
    }, function () {
      args.config = "denied"
      $('#main').template('store', args)
        .find('.paginate .load-more').trigger('click');
    });
    break; /* #!/stores/store */
    // }}}
  case '#!/stores/new': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    if (!AEGIS.is('tenant', 'engineer')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'engineer' });
      break;
    }
    $('#main').template('loading');
    var data = { type: 'store' };
    $('#main').html($($.template('stores-form', data))
      .autofocus()
      .on('change', 'select[name="store.plugin"]', function (event) {
        data.plugin = $(event.target).val();
        console.log(data);
        $('#main .redraw.store').template('plugin-form-agent-selector', data);
      })
      .on('change', 'select[name="store.agent"]', function (event) {
        data.agent = $(event.target).val();
        console.log(data);
        $('#main .redraw.store').template('plugin-form-agent-selector', data);
      })
      .on('submit', 'form', function (event) {
        event.preventDefault();

        var $form = $(event.target);
        if (!$form.reset().validate().isOK()) { return; }
        var data = $form.serializeObject().store;
        data.threshold = readableToBytes(data.threshold);

        $form.submitting(true);
        api({
          type: 'POST',
          url:  '/v2/tenants/'+AEGIS.current.uuid+'/stores',
          data: data,
          success: function () {
            goto("#!/stores");
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
      }));
    break; /* #!/stores */
    // }}}
  case '#!/stores/edit': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    if (!AEGIS.is('tenant', 'engineer')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'engineer' });
      break;
    }
    $('#main').html($($.template('stores-form', {
        store: AEGIS.store(args.uuid)
      }))
      .autofocus()
      .on('submit', 'form', function (event) {
        event.preventDefault();

        var $form = $(event.target);
        if (!$form.reset().validate().isOK()) { return; }
        var data = $form.serializeObject().store;
        data.threshold = readableToBytes(data.threshold);

        $form.submitting(true);
        api({
          type: 'PUT',
          url:  '/v2/tenants/'+AEGIS.current.uuid+'/stores/'+args.uuid,
          data: data,
          success: function () {
            goto("#!/stores/store:uuid:"+args.uuid);
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
      }));

    break; /* #!/stores/edit */
    // }}}
  case '#!/stores/test': /* {{{ */
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    api({
        type: 'GET',
        url:  '/v2/tenants/'+AEGIS.current.uuid+'/stores/'+args.uuid,
        error: "Failed to retrieve storage system information from the SHIELD API.",
        success: function (store) {
          modal($($.template('stores-test', { store: store }))
            .on('click', '[rel="yes"]', function (event) {
              event.preventDefault();
              api({
                type: 'POST',
                url:  '/v2/tenants/'+AEGIS.current.uuid+'/stores/'+args.uuid+'/test',
                complete: function () {
                modal(true);
                },
                success: function (event) {
                  goto('#!/stores/store:uuid:');
                  banner('Started storage health check.');
                },
                error: function (event) {
                  banner('Unable to test the storage system');
                }
              });
            })
            .on('click', '[rel="close"]', function (event) {
            modal(true);
            goto('#!/stores/store:uuid:'+args.uuid);
            })
          );
        }
    });
    break; /* #!/stores/test */
    // // }}}  
  case '#!/stores/delete': /* {{{ */
    if (!AEGIS.current) {
      $('#main').template('you-have-no-tenants');
      break;
    }
    if (!AEGIS.is('tenant', 'engineer')) {
      $('#main').template('access-denied', { level: 'tenant', need: 'engineer' });
      break;
    }
    api({
      type: 'GET',
      url:  '/v2/tenants/'+AEGIS.current.uuid+'/stores/'+args.uuid,
      error: "Failed to retrieve storage system information from the SHIELD API.",
      success: function (store) {
        modal($($.template('stores-delete', { store: store }))
          .on('click', '[rel="yes"]', function (event) {
            event.preventDefault();
            api({
              type: 'DELETE',
              url:  '/v2/tenants/'+AEGIS.current.uuid+'/stores/'+args.uuid,
              error: "Unable to delete storage system",
              complete: function () {
                modal(true);
              },
              success: function (event) {
                goto('#!/stores');
              }
            });
          })
          .on('click', '[rel="close"]', function (event) {
            modal(true);
            goto('#!/stores/store:uuid:'+args.uuid);
          })
        );
      }
    });

    break; /* #!/stores/delete */
    // }}}

  case '#!/tenants/edit': /* {{{ */
    if (!AEGIS.current) {
        $('#main').template('you-have-no-tenants');
        break;
    }
    if (!AEGIS.is(args.uuid, 'admin')) {
        $('#main').template('access-denied', { level: 'tenant', need: 'admin' });
        break;
    }
    api({
      type: 'GET',
      url:  '/v2/tenants/'+args.uuid,
      error: "Failed to retrieve tenant information from the SHIELD API.",
      success: function (data) {
        var members = {};
        $.each(data.members, function (i, user) {
          members[user.uuid] = user;
        });
        $('#main').html($($.template('tenants-form', { tenant: data, admin: false }))
          .userlookup('input[name=invite]', {
            filter: function (users) {
              var lst = [];
              $.each(users, function (i, user) {
                if (!(user.uuid in members)) {
                  lst.push(user);
                }
              });
              return lst;
            },
            onclick: function (user) {
              user.role = 'operator';
              $('#main table tbody').append($.template('tenants-form-invitee', { user: user }));
              members[user.uuid] = user;

              api({
                type: 'POST',
                url:  '/v2/tenants/'+args.uuid+'/invite',
                data: {users:[user]},
                error: "Unable to save tenant role assignment.",
                success: function () {
                  banner('User "'+user.account+'" is now '+{
                      admin    : 'an administrator',
                      engineer : 'an engineer',
                      operator : 'an operator'
                    }[user.role]+' on this tenant.');
                }
              });
            }
          })
          .roles('.role', function (e, role) {
            var data = {
              uuid    : e.extract('uuid'),
              account : e.extract('account'),
              role    : role
            };
            api({
              type: 'POST',
              url:  '/v2/tenants/'+args.uuid+'/invite',
              data: {users:[data]},
              error: "Unable to save tenant role assignment.",
              success: function () {
                banner('User "'+data.account+'" is now '+{
                    admin    : 'an administrator',
                    engineer : 'an engineer',
                    operator : 'an operator'
                  }[data.role]+' on this tenant.');
              }
            });
          })
          .autofocus()
          .on('click', 'a[href="banish:user"]', function (event) {
            event.preventDefault();

            var e = $(event.target);
            var data = {
              uuid    : e.extract('uuid'),
              account : e.extract('account')
            };
            delete members[data.uuid];
            api({
              type: 'POST',
              url:  '/v2/tenants/'+args.uuid+'/banish',
              data: {users:[data]},
              error: "Unable to save tenant role assignment.",
              success: function () {
                banner('User "'+data.account+'" is no longer associated with this tenant.');
              }
            })
            $(event.target).closest('tr').remove();
          }));
      }
    });

    break; /* #!/tenants/edit */
    // }}}

  case '#!/admin': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').template('admin');
    break; /* #!/admin */
    // }}}
  case '#!/admin/agents': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    Scratch.track('redrawable', false);
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/agents',
      error: "Failed retrieving the list of agents from the SHIELD API.",
      success: function (data) {
        $('#main').html($($.template('agents', data))
          .on('click', 'a[rel]', function (event) {
            event.preventDefault();

            var action = $(event.target).closest('a[rel]').attr('rel');
            if (action == 'hide' || action == 'show') {
              api({
                type: 'POST',
                url:  '/v2/agents/'+$(event.target).extract('agent-uuid')+'/'+action,
                error: "Unable to "+action+" agent via the SHIELD API.",
                success: function () { reload(); }
              });

            } else if (action == 'resync') {
              api({
                type: 'POST',
                url:  '/v2/agents/'+$(event.target).extract('agent-uuid')+'/resync',
                error: "Resynchronization request failed",
                success: function () {
                  banner("Resynchronization of agent underway");
                }
              });
            } else if (action == 'delete') {
              var agent_uuid = $(event.target).extract('agent-uuid');
              modal($($.template('agents-delete', { agent: data.agents[0] }))
              .on('click', '[rel="yes"]', function (event) {
                event.preventDefault();
                api({
                  type: 'DELETE',
                  url:  '/v2/agents/'+agent_uuid,
                  error: "Unable to delete agent",
                  complete: function () {
                    modal(true);
                  },
                  success: function (event) {
                    goto('#!/admin/agents');
                  }
                });
              })
              .on('click', '[rel="close"]', function (event) {
                  modal(true);
                  goto('#!/admin/agents');
                })
              );
            }
          }));
      }
    });
    break; /* #!/admin/agents */
    // }}}
  case '#!/admin/auth': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/auth/providers',
      error: "Failed retrieving the list of configured authentication providers from the SHIELD API.",
      success: function (data) {
        $('#main').template('auth-providers', { providers: data });
      }
    });
    break; /* #!/admin/auth */
    // }}}
  case '#!/admin/auth/config': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/auth/providers/'+args.name,
      error: "Failed retrieving the authentication provider configuration from the SHIELD API.",
      success: function (data) {
        $('#main').template('auth-provider-config', { provider: data });
      }
    });
    break; /* #!/admin/auth */
    // }}}
  case '#!/admin/rekey': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').html($($.template('rekey')))
      .autofocus()
      .on('submit', 'form', function (event) {
        event.preventDefault();

        var $form = $(event.target);
        var data = $form.serializeObject();

        $form.reset();
        if (data.current == "") {
          $form.error('current', 'missing');
        }

        if (data.new == "") {
          $form.error('new', 'missing');

        } else if (data.confirm == "") {
          $form.error('confirm', 'missing');

        } else if (data.new != data.confirm) {
          $form.error('confirm', 'mismatch');
        }

        data.rotate_fixed_key = !!data.rotate_fixed_key;

        if (!$form.isOK()) {
          return;
        }

        delete data.confirm;
        $form.submitting(true);
        api({
          type: 'POST',
          url:  '/v2/rekey',
          data: data,
          success: function (data) {
            if (data.fixed_key != "") {
              $('#viewport').template('fixedkey', data);
            } else {
              goto("#!/admin");
            }
            banner('Succcessfully rekeyed the SHIELD Core.');
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
      });

    break; /* #!/admin/rekey */
    // }}}

  case '#!/admin/tenants': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/tenants',
      error: 'Failed to retrieve tenant information from the SHIELD API.',
      success: function (data) {
        $('#main').template('tenants', { tenants: data, admin: true });
      }
    });
    break; /* #!/admin/tenants */
    // }}}
  case '#!/admin/tenants/new': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('manager')) {
      $('#main').template('access-denied', { level: 'system', need: 'manager' });
      break;
    }
    var members = {};

    $('#main').html($($.template('tenants-form', { policy: null, admin: true }))
      .userlookup('input[name=invite]', {
        // {{{
        filter: function (users) {
          var lst = [];
          $.each(users, function (i, user) {
            if (!(user.uuid in members)) {
              lst.push(user);
            }
          });
          return lst;
        },
        onclick: function (user) {
          user.role = 'operator';
          $('#main table tbody').append($.template('tenants-form-invitee', { user: user }));
          members[user.uuid] = user.role;
        }
        // }}}
      })
      .roles('.role', function (e, role) {
        members[e.extract('uuid')] = role;
      })
      .autofocus()
      .on('click', 'a[href="banish:user"]', function (event) {
        // {{{
        event.preventDefault();
        delete members[$(event.target).extract('uuid')];
        $(event.target).closest('tr').remove();
        // }}}
      })
      .on('submit', 'form', function (event) {
        // {{{
        event.preventDefault();

        var $form = $(event.target);
        var data = $form.serializeObject();
        data.users = [];
        for (uuid in members) {
          data.users.push({
            uuid: uuid,
            role: members[uuid]
          });
        }

        $form.reset();
        $form.submitting(true);
        api({
          type: 'POST',
          url:  '/v2/tenants',
          data: data,
          success: function () {
            goto("#!/admin/tenants");
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
        // }}}
      }));

    break; /* #!/admin/tenants/new */
    // }}}
  case '#!/admin/tenants/edit': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('manager')) {
      $('#main').template('access-denied', { level: 'system', need: 'manager' });
      break;
    }
    api({
      type: 'GET',
      url:  '/v2/tenants/'+args.uuid,
      error: "Failed to retrieve tenant information from the SHIELD API.",
      success: function (data) {
        var members = {};
        $.each(data.members, function (i, user) {
          members[user.uuid] = user;
        });
        $('#main').html($($.template('tenants-form', { tenant: data, admin: true }))
          .userlookup('input[name=invite]', {
            filter: function (users) {
              var lst = [];
              $.each(users, function (i, user) {
                if (!(user.uuid in members)) {
                  lst.push(user);
                }
              });
              return lst;
            },
            onclick: function (user) {
              user.role = 'operator';
              $('#main table tbody').append($.template('tenants-form-invitee', { user: user }));
              members[user.uuid] = user;

              api({
                type: 'POST',
                url:  '/v2/tenants/'+args.uuid+'/invite',
                data: {users:[user]},
                error: "Unable to save tenant role assignment.",
                success: function () {
                  banner('User "'+user.account+'" is now '+{
                      admin    : 'an administrator',
                      engineer : 'an engineer',
                      operator : 'an operator'
                    }[user.role]+' on this tenant.');
                }
              });
            }
          })
          .roles('.role', function (e, role) {
            var data = {
              uuid    : e.extract('uuid'),
              account : e.extract('account'),
              role    : e.extract('role')
            };
            api({
              type: 'POST',
              url:  '/v2/tenants/'+args.uuid+'/invite',
              data: {users:[data]},
              error: "Unable to save tenant role assignment.",
              success: function () {
                banner('User "'+data.account+'" is now '+{
                    admin    : 'an administrator',
                    engineer : 'an engineer',
                    operator : 'an operator'
                  }[data.role]+' on this tenant.');
              }
            });
          })
          .autofocus()
          .on('click', 'a[href="banish:user"]', function (event) {
            event.preventDefault();

            var e = $(event.target);
            var data = {
              uuid    : e.extract('uuid'),
              account : e.extract('account')
            };
            delete members[data.uuid];
            api({
              type: 'POST',
              url:  '/v2/tenants/'+args.uuid+'/banish',
              data: {users:[data]},
              error: "Unable to save tenant role assignment.",
              success: function () {
                banner('User "'+data.account+'" is no longer associated with this tenant.');
              }
            })
            $(event.target).closest('tr').remove();
          })
          .on('submit', 'form', function (event) {
            event.preventDefault();

            var $form = $(event.target);
            var data = $form.serializeObject();

            $form.reset();
            $form.submitting(true);
            api({
              type: 'PATCH',
              url:  '/v2/tenants/'+args.uuid,
              data: data,
              success: function () {
                goto("#!/admin/tenants");
              },
              error: function (xhr) {
                $form.submitting(false);
                $form.error(xhr.responseJSON);
              }
            });
          }));
      }
    });

    break; /* #!/admin/tenants/edit */
    // }}}

  case '#!/admin/users': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/auth/local/users',
      error: "Failed retrieving the list of local SHIELD users from the SHIELD API.",
      success: function (data) {
        $('#main').template('admin-users', { users: data });
      }
    });
    break; /* #!/admin/users */
    // }}}
  case "#!/admin/users/new": /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('manager')) {
      $('#main').template('access-denied', { level: 'system', need: 'manager' });
      break;
    }
    $('#main').html($($.template('admin-users-new', {}))
      .autofocus()
      .on('submit', 'form', function (event) {
        event.preventDefault();
        var $form = $(event.target);

        var payload = {
          name:     $form.find('[name=name]').val(),
          sysrole:  $form.find('[name=sysrole]').val(),
          account:  $form.find('[name=account]').val(),
          password: $form.find('[name=password]').val()
        };

        if ($form.find('[name=confirm]').val() != payload.password) {
          banner("Passwords don't match", "error");
          return;
        }

        banner("Creating new user...", "info");
        $form.submitting(true);
        api({
          type: 'POST',
          url:  '/v2/auth/local/users',
          data: payload,
          success: function (data) {
            banner('New user created successfully.');
            goto("#!/admin/users");
          },
          error: function (xhr) {
            $form.submitting(false);
            banner("Failed to create new user", "error");
          }
        });
      }));
    break; // #!/admin/users/new
    // }}}
  case "#!/admin/users/edit": /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('manager')) {
      $('#main').template('access-denied', { level: 'system', need: 'manager' });
      break;
    }
    api({
      type: 'GET',
      url:  '/v2/auth/local/users/'+args.uuid,
      error: "Unable to retrieve user information from the SHIELD API.",
      success: function (data) {
        $('#main').html($($.template('admin-users-edit', { user: data }))
          .autofocus()
          .on('submit', 'form', function (event) {
            event.preventDefault();
            var $form = $(event.target);
            var data = $form.serializeObject();

            $form.reset();
            if (data.password != data.confirm) {
              $form.error('confirm', 'mismatch');
            }

            if (!$form.isOK()) {
              return;
            }
            delete data.confirm;

            if ($form.find('[name=password]').val()==""){
              var payload = {
                name:    $form.find('[name=name]').val(),
                sysrole: $form.find('[name=sysrole]').val(),
              };
            } else {
              var payload = {
                name:    $form.find('[name=name]').val(),
                sysrole: $form.find('[name=sysrole]').val(),
                password: $form.find('[name=password]').val()
              };
            }

            banner("Updating user...", "info");
            $form.submitting(true);
            api({
              type: 'PATCH',
              url:  '/v2/auth/local/users/'+args.uuid,
              data: payload,
              success: function (data) {
                banner('User updated successfully.');
                goto("#!/admin/users");
              },
              error: function (xhr) {
                $form.submitting(false);
                banner("Failed to update user", "error");
              }
            });
          }));
      }
    });
    break; // #!/admin/users/new
    // }}}

  case '#!/admin/stores': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    $('#main').template('stores', {
      admin: true,
      stores: AEGIS.stores({ global: true })
    });
    break; /* #!/admin/stores */
    // }}}
  case '#!/admin/stores/store': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    args.admin = true;
    $('#main').template('store', args);
    api({
      type:     'GET',
      url:      '/v2/global/stores/'+args.uuid+'/config',
      dataType: 'json'
    }, true).then(function (config) {
      args.config = config || []
      $('#main').template('store', args)
        .find('.paginate .load-more').trigger('click');
    }, function () {
      args.config = "denied"
      $('#main').template('store', args)
        .find('.paginate .load-more').trigger('click');
    });
    break; /* #!/admin/stores/store */
    // }}}
  case '#!/admin/stores/new': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    var data = { type: 'store' };
    $('#main').html($($.template('stores-form', { admin:  true }))
      .autofocus()
      .on('change', 'select[name="store.plugin"]', function (event) {
        data.plugin = $(event.target).val();
        console.log(data);
        $('#main .redraw.store').template('plugin-form-agent-selector', data);
      })
      .on('change', 'select[name="store.agent"]', function (event) {
        data.agent = $(event.target).val();
        console.log(data);
        $('#main .redraw.store').template('plugin-form-agent-selector', data);
      })
      .on('submit', 'form', function (event) {
        event.preventDefault();

        var $form = $(event.target);
        if (!$form.reset().validate().isOK()) { return; }

        var data = $form.serializeObject().store;
        data.threshold = readableToBytes(data.threshold);

        $form.submitting(true);
        api({
          type: 'POST',
          url:  '/v2/global/stores',
          data: data,
          success: function () {
            goto("#!/admin/stores");
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
      }));

    break; /* #!/admin/stores */
    // }}}
  case '#!/admin/stores/edit': /* {{{ */
    Scratch.track('redrawable', false);
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    var data = {
      admin: true,
      store: AEGIS.store(args.uuid)
    };
    $('#main').html($($.template('stores-form', data))
      .autofocus()
      .on('submit', 'form', function (event) {
        event.preventDefault();

        var $form = $(event.target);
        if (!$form.reset().validate().isOK()) { return; }

        var data = $form.serializeObject().store;
        data.threshold = readableToBytes(data.threshold);

        $form.submitting(true);
        api({
          type: 'PUT',
          url:  '/v2/global/stores/'+args.uuid,
          data: data,
          success: function () {
            goto("#!/admin/stores/store:uuid:"+args.uuid);
          },
          error: function (xhr) {
            $form.submitting(false);
            $form.error(xhr.responseJSON);
          }
        });
      }));

    break; /* #!/admin/stores/edit */
    // }}}
  case '#!/admin/stores/delete': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    api({
      type: 'GET',
      url:  '/v2/global/stores/'+args.uuid,
      error: "Failed to retrieve storage system information from the SHIELD API.",
      success: function (store) {
        modal($($.template('stores-delete', { store: store }))
          .on('click', '[rel="yes"]', function (event) {
            event.preventDefault();
            api({
              type: 'DELETE',
              url:  '/v2/global/stores/'+args.uuid,
              error: "Unable to delete storage system",
              complete: function () {
                modal(true);
              },
              success: function (event) {
                goto('#!/admin/stores');
              }
            });
          })
          .on('click', '[rel="close"]', function (event) {
            modal(true);
            goto('#!/admin/stores/store:uuid:'+args.uuid);
          })
        );
      }
    });

    break; /* #!/admin/stores/delete */
    // }}}

  case '#!/admin/sessions': /* {{{ */
    if (!AEGIS.is('admin')) {
      $('#main').template('access-denied', { level: 'system', need: 'admin' });
      break;
    }
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/auth/sessions',
      error: "Failed retrieving the list of sessions from the SHIELD API.",
      success: function (data) {
        data = data.sort(function(a, b) {
          if (a.user_account != b.user_account){
              return a.user_account > b.user_account;
          }
          return tparse(a.last_seen_at).getTime() < tparse(b.last_seen_at).getTime();
        });
        $('#main').template('sessions', { sessions: data, admin: true });
      }
    });
    break; /* #!/admin/sessions */
    // }}}
  case '#!/admin/sessions/delete': /* {{{ */
    if (!AEGIS.is('admin')) {
      $('#main').template('access-denied', { level: 'system', need: 'admin' });
      break;
    }
    api({
      type: 'GET',
      url:  '/v2/auth/sessions/'+args.uuid,
      error: "Failed to retrieve session information from the SHIELD API.",
      success: function (data) {
      modal($($.template('sessions-delete', { session: data }))
        .on('click', '[rel="yes"]', function (event) {
        event.preventDefault();
        api({
            type: 'DELETE',
            url:  '/v2/auth/sessions/'+args.uuid,
            error: "Unable to delete session",
            complete: function () {
            modal(true);
            },
            success: function (event) {
            goto('#!/admin/sessions');
            }
        });
        })
        .on('click', '[rel="close"]', function (event) {
        modal(true);
        goto('#!/admin/sessions');
        })
    );
    }
    });
    break; /* #!/admin/sessions/delete */
    // }}}

  case '#!/admin/fixups': /* {{{ */
    if (!AEGIS.is('engineer')) {
      $('#main').template('access-denied', { level: 'system', need: 'engineer' });
      break;
    }
    Scratch.track('redrawable', false);
    $('#main').template('loading');
    api({
      type: 'GET',
      url:  '/v2/fixups',
      error: "Failed retrieving the list of data fixups from the SHIELD API.",
      success: function (data) {
        $('#main').template('fixups', { fixups: data })
          .on('click', 'a[href^="apply-fixup:"]', function (event) {
            event.preventDefault();

            var a = $(event.target).closest('a[href^="apply-fixup:"]');
            var id = a.attr('href').replace(/^apply-fixup:/, '')
            api({
              type: 'POST',
              url:  '/v2/fixups/'+id+'/apply',
              error: 'Unable to apply data fixup "'+id+'".',
              success: function () {
                banner('Fixup "'+id+'" applied successfully!');
                reload();
              }
            });
          });
      }
    });
    break; /* #!/admin/fixups */
    // }}}

  default: /* 404 {{{ */
    $('#main').template('404', {
      wanted:  page,
      args:    argv,
      referer: referer,
    });
    return; /* 404 */
    // }}}
  }
  referer = page;
}

function goto(page) {
  if (document.location.hash == page) {
    $(document.body).trigger('shield:navigate', page);
  } else {
    document.location.hash = page;
  }
}
function reload() {
  goto(document.location.hash);
}

$(function () {
  $('#viewport').template('loading');
  window.AEGIS = $.aegis();
  window.AEGIS.subscribe()
    .then(
      function () {
        document.title = "SHIELD "+AEGIS.shield.env;
        $('.top-bar').template('top-bar');
        if (AEGIS.authenticated()) {
          $('#viewport').template('layout');
          if (AEGIS.vault != "uninitialized") {
            $('#side-bar').template('side-bar');
          }
          $('#hud').template('hud');
          if (AEGIS.vault == "locked") {
            $('#lock-state').fadeIn();
          }
          if (AEGIS.vault != "uninitialized") {
            $('#side-bar').template('side-bar')
          }
        }
        $(document.body)
          .on('shield:navigate', function (event, to) {
            api(); // clear outstanding API calls
            dispatch(to);
          })
          .on('click', 'a[href^="#"]:not([rel])', function (event) {
            goto($(event.target).closest('[href]').attr('href'));
          });
        $(window).on('hashchange', function (event) {
          $(document.body).trigger('shield:navigate', document.location.hash);
        }).trigger('hashchange');
      },
      function () {
        console.log('AEGIS subscription setup failed, redirecting to login page...');
        dispatch("#!/login");
      });
});
